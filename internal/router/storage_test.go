package router

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-seerr/internal/seerr"
)

const gb = float64(bytesPerGB)

func TestPickFolder(t *testing.T) {
	folders := []seerr.RootFolder{
		{Path: "/a", FreeSpace: 10 * gb},
		{Path: "/b", FreeSpace: 300 * gb},
		{Path: "/c", FreeSpace: 900 * gb},
		{Path: "/ext", FreeSpace: 80 * gb},
		{Path: "/last", FreeSpace: 60 * gb},
	}
	config := func(mode1 string) storageConfig {
		return storageFromConfig(map[string]any{
			"storage_groups_enabled":         true,
			"storage_min_free_gb":            float64(50),
			"storage_group_1_name":           "Main",
			"storage_group_1_mode":           mode1,
			"storage_group_1_radarr_folders": []any{"/a", "/b", "/c", "/gone"},
			"storage_group_2_radarr_folders": []any{"/ext"},
			"storage_group_8_name":           "Fallback",
			"storage_group_8_radarr_folders": []any{"/last"},
		})
	}
	tests := []struct {
		name      string
		mode      string
		start     int
		free      map[string]float64 // overrides
		want      string
		wantGroup string
	}{
		{name: "in order skips the full first folder", mode: storageModeInOrder, want: "/b", wantGroup: "Main"},
		{name: "distribute takes the most free space", mode: storageModeDistribute, want: "/c", wantGroup: "Main"},
		{name: "a full group hands over to the next", mode: storageModeInOrder,
			free: map[string]float64{"/b": 0, "/c": 49 * gb}, want: "/ext", wantGroup: "Group 2"},
		{name: "a start group skips earlier groups", mode: storageModeInOrder, start: 2, want: "/ext", wantGroup: "Group 2"},
		{name: "a full start group hands over to later groups only", mode: storageModeInOrder, start: 2,
			free: map[string]float64{"/ext": 1 * gb}, want: "/last", wantGroup: "Fallback"},
		{name: "exactly the minimum counts as room", mode: storageModeInOrder,
			free: map[string]float64{"/a": 50 * gb}, want: "/a", wantGroup: "Main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := append([]seerr.RootFolder(nil), folders...)
			for i := range fs {
				if v, ok := tt.free[fs[i].Path]; ok {
					fs[i].FreeSpace = v
				}
			}
			path, group, err := config(tt.mode).pickFolder("radarr", tt.start, fs)
			if err != nil || path != tt.want || group.label() != tt.wantGroup {
				t.Fatalf("got %q from %q err=%v, want %q from %q", path, group.label(), err, tt.want, tt.wantGroup)
			}
		})
	}

	t.Run("every group full is an error", func(t *testing.T) {
		_, _, err := config(storageModeInOrder).pickFolder("radarr", 8, []seerr.RootFolder{{Path: "/last", FreeSpace: gb}})
		if err == nil || !strings.Contains(err.Error(), "50 GB") {
			t.Fatalf("want an error naming the minimum, got %v", err)
		}
	})
}

func TestStorageFromConfigDefaults(t *testing.T) {
	st := storageFromConfig(nil)
	if st.Enabled || st.MinFreeBytes != defaultStorageMinFreeGB*gb || len(st.Groups) != storageGroupSlots {
		t.Fatalf("defaults: %+v", st)
	}
	st = storageFromConfig(map[string]any{"storage_groups_enabled": true, "storage_min_free_gb": "-1"})
	if st.Errs[keyStorageMinFree] == "" {
		t.Fatalf("a negative minimum must be reported: %+v", st)
	}
	// On without folders for a kind leaves that kind to Seerr.
	if st.manages("radarr") {
		t.Fatalf("no group lists a Radarr folder")
	}
}

// groupedConfig turns storage groups on for movies on server 0, whose folders
// are /movies (50 GB free), /movies-uhd (2 TB free) and /tv (none reported).
func groupedConfig(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"storage_groups_enabled":         true,
		"storage_min_free_gb":            float64(100),
		"storage_group_1_name":           "Main storage",
		"storage_group_1_radarr_folders": []any{"/movies", "/tv"},
		"storage_group_2_name":           "External",
		"storage_group_2_radarr_folders": []any{"/movies-uhd"},
		// The root folder select is hidden while groups are on; a value left
		// in the config must not be used.
		"radarr_root_folder": "/movies",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestFulfillChoosesFolderFromStorageGroups(t *testing.T) {
	stub := &multiServerSeerr{}
	resp := fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(nil)))
	if len(resp.GetTargets()) != 2 || resp.GetTargets()[0].GetStatus() != "queued" {
		t.Fatalf("targets: %+v msg=%q", resp.GetTargets(), resp.GetMessage())
	}
	// Main storage has no folder with 100 GB free, so External takes it.
	if len(stub.bodies) != 1 || stub.bodies[0]["rootFolder"] != "/movies-uhd" {
		t.Fatalf("body: %+v", stub.bodies)
	}

	// With a lower minimum the first group's first folder has room.
	stub = &multiServerSeerr{}
	fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(map[string]any{"storage_min_free_gb": float64(20)})))
	if len(stub.bodies) != 1 || stub.bodies[0]["rootFolder"] != "/movies" {
		t.Fatalf("body: %+v", stub.bodies)
	}

	// A picked group is where the search starts.
	stub = &multiServerSeerr{}
	fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(map[string]any{
		"storage_min_free_gb": float64(20), "radarr_storage_group": "2",
	})))
	if len(stub.bodies) != 1 || stub.bodies[0]["rootFolder"] != "/movies-uhd" {
		t.Fatalf("body: %+v", stub.bodies)
	}

	// A group is picked by name, so the pick follows it to another slot.
	stub = &multiServerSeerr{}
	fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), map[string]any{
		"storage_groups_enabled": true, "storage_min_free_gb": float64(20), "radarr_storage_group": "External",
		"storage_group_1_name": "Main storage", "storage_group_1_radarr_folders": []any{"/movies"},
		"storage_group_5_name": "External", "storage_group_5_radarr_folders": []any{"/movies-uhd"},
	}))
	if len(stub.bodies) != 1 || stub.bodies[0]["rootFolder"] != "/movies-uhd" {
		t.Fatalf("body: %+v", stub.bodies)
	}
}

func TestFulfillFailsWhenEveryStorageGroupIsFull(t *testing.T) {
	stub := &multiServerSeerr{}
	resp := fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(map[string]any{"storage_min_free_gb": float64(5000)})))
	if len(stub.bodies) != 0 {
		t.Fatalf("no request may be sent: %+v", stub.bodies)
	}
	for _, tgt := range resp.GetTargets() {
		if tgt.GetStatus() != "failed" || !strings.Contains(tgt.GetMessage(), "5000 GB free") {
			t.Fatalf("target: %+v", tgt)
		}
	}
}

func TestFulfillLeavesUncoveredKindToSeerr(t *testing.T) {
	// No group lists a Sonarr folder, so series go to Seerr's own folder,
	// even when the hidden root folder select still holds a value.
	stub := &multiServerSeerr{}
	fulfill(t, "series", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(map[string]any{"sonarr_root_folder": "/tv"})))
	if len(stub.bodies) != 1 {
		t.Fatalf("bodies: %+v", stub.bodies)
	}
	if _, set := stub.bodies[0]["rootFolder"]; set {
		t.Fatalf("series must keep Seerr's folder: %+v", stub.bodies[0])
	}
}

func TestListConfigOptionsOffersGroupsInsteadOfFolders(t *testing.T) {
	stub := &multiServerSeerr{}
	resp, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{
		Connection: serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(nil)),
	})
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	by := resp.GetOptionsByField()
	if got := optionLabels(by["radarr_storage_group"]); got != "auto=Automatic (groups in order)|Main storage=Main storage|External=External" {
		t.Fatalf("radarr_storage_group: %s", got)
	}
	if _, ok := by["radarr_root_folder"]; ok {
		t.Fatalf("radarr_root_folder must have no choices while groups cover movies")
	}
	// No group covers series, so neither select has choices: Seerr keeps
	// its own folder.
	for _, key := range []string{"sonarr_storage_group", "sonarr_root_folder"} {
		if _, ok := by[key]; ok {
			t.Fatalf("%s must have no choices", key)
		}
	}
}

func TestValidateChecksStorageGroups(t *testing.T) {
	validate := func(cfg map[string]any) map[string]string {
		t.Helper()
		resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{
			Connection: serverConn(t, newMultiServerSeerr(t, &multiServerSeerr{}), cfg),
		})
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		return resp.GetFieldErrors()
	}
	if fe := validate(groupedConfig(nil)); len(fe) != 0 {
		t.Fatalf("valid groups flagged: %v", fe)
	}
	// Full groups do not block saving.
	if fe := validate(groupedConfig(map[string]any{"storage_min_free_gb": float64(5000)})); len(fe) != 0 {
		t.Fatalf("full groups must not block saving: %v", fe)
	}
	fe := validate(groupedConfig(map[string]any{
		"storage_group_2_radarr_folders": []any{"/movies-uhd", "/nope"},
		"radarr_storage_group":           "3", // group 3 has no folders
		"storage_min_free_gb":            "lots",
		"storage_group_4_name":           "External", // group 2's name
	}))
	for _, key := range []string{"storage_group_2_radarr_folders", "radarr_storage_group", "storage_min_free_gb", "storage_group_4_name"} {
		if fe[key] == "" {
			t.Fatalf("%s: want field error, got %v", key, fe)
		}
	}
}

func TestTestConnectionReportsFullStorageGroups(t *testing.T) {
	resp, err := New().TestConnection(context.Background(), &pluginv1.TestConnectionRequest{
		Connection: serverConn(t, newMultiServerSeerr(t, &multiServerSeerr{}), groupedConfig(map[string]any{"storage_min_free_gb": float64(5000)})),
	})
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if resp.GetOk() || !strings.Contains(resp.GetMessage(), "connected, but no Radarr folder of the storage groups") {
		t.Fatalf("want a connected-but-full failure, got %+v", resp)
	}
}

// Storage groups hold the standard servers' folders, so with a separate 4K
// server only the standard request takes a folder from them.
func TestStorageGroupsLeave4KRequestAlone(t *testing.T) {
	stub := &multiServerSeerr{}
	resp := fulfill(t, "movie", hdUHD, serverConn(t, newMultiServerSeerr(t, stub), groupedConfig(map[string]any{"supports_4k": true})))
	if len(resp.GetTargets()) != 2 || len(stub.bodies) != 2 {
		t.Fatalf("want two requests: targets=%v bodies=%v", resp.GetTargets(), stub.bodies)
	}
	if stub.bodies[0]["is4k"] != false || stub.bodies[0]["rootFolder"] != "/movies-uhd" {
		t.Fatalf("standard request: %+v", stub.bodies[0])
	}
	if _, set := stub.bodies[1]["rootFolder"]; stub.bodies[1]["is4k"] != true || set {
		t.Fatalf("4K request must keep Seerr's folder: %+v", stub.bodies[1])
	}
}
