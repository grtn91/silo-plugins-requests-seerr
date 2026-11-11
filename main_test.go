package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestEmbeddedManifestLoads(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if m.GetPluginId() != "silo.requests.seerr" {
		t.Fatalf("plugin_id: want silo.requests.seerr got %q", m.GetPluginId())
	}
	caps := m.GetCapabilities()
	if len(caps) != 1 {
		t.Fatalf("want 1 capability, got %d", len(caps))
	}
	if caps[0].GetType() != "request_router.v1" || caps[0].GetId() != "seerr" {
		t.Fatalf("capability: want request_router.v1/seerr got %q/%q", caps[0].GetType(), caps[0].GetId())
	}
	rr := caps[0].GetRequestRouter()
	if !rr.GetReportsDownloadProgress() {
		t.Fatalf("request_router.reports_download_progress: want true")
	}
	// Seerr requests are whole-series ("seasons": "all"), so the plugin must
	// not receive season-only requests.
	if rr.GetSupportsSeasons() {
		t.Fatalf("request_router.supports_seasons: want false")
	}

	// The destination picks are dynamic selects (so Silo's route editor
	// offers them as per-rule overrides) whose "default" value keeps Seerr's
	// own choice. No option may have an empty value: Silo's route editor
	// cannot render one.
	destination := []string{
		"radarr_server_id", "radarr_profile_id", "radarr_root_folder",
		"sonarr_server_id", "sonarr_profile_id", "sonarr_root_folder",
	}
	// The same picks for the 4K request of a Seerr with a separate 4K server.
	destination4K := []string{
		"radarr_4k_server_id", "radarr_4k_profile_id", "radarr_4k_root_folder",
		"sonarr_4k_server_id", "sonarr_4k_profile_id", "sonarr_4k_root_folder",
	}
	fields := map[string]bool{}
	for _, f := range caps[0].GetConfigSchema()[0].GetAdminForm().GetFields() {
		fields[f.GetKey()] = true
		if f.GetKey() == "supports_4k" {
			// Silo reads this key: off, it does not ask the connection for a
			// 2160p copy on its own. Off must stay the default.
			if f.GetControl().String() != "ADMIN_FORM_CONTROL_SWITCH" || f.GetDefaultValue().GetBoolValue() {
				t.Fatalf("supports_4k: want a switch that is off by default")
			}
		}
		is4K := slices.Contains(destination4K, f.GetKey())
		if !is4K && !slices.Contains(destination, f.GetKey()) {
			continue
		}
		// The 4K picks show only with a separate 4K server.
		if show := f.GetShowWhen(); is4K && (len(show) != 1 || show[0].GetField() != "supports_4k" || !slices.Equal(show[0].GetEquals(), []string{"true"})) {
			t.Fatalf("%s: want it shown only while supports_4k is on", f.GetKey())
		}
		if f.GetControl().String() != "ADMIN_FORM_CONTROL_SELECT" || !f.GetDynamicOptions() {
			t.Fatalf("%s: want a dynamic select, got %v dynamic=%v", f.GetKey(), f.GetControl(), f.GetDynamicOptions())
		}
		if f.GetDefaultValue().GetStringValue() != "default" || len(f.GetOptions()) != 1 || f.GetOptions()[0].GetValue() != "default" {
			t.Fatalf("%s: want a \"default\" default with one static Seerr default option", f.GetKey())
		}
	}
	for _, key := range append([]string{"supports_4k"}, append(destination, destination4K...)...) {
		if !fields[key] {
			t.Fatalf("admin form is missing %s", key)
		}
	}
	// Requester options are read by hosts whose SDK knows them; this SDK
	// drops the field, so it is checked on the raw manifest. Each must name a
	// destination select and its "chosen by" select.
	var raw struct {
		Capabilities []struct {
			RequestRouter struct {
				RequesterOptions []struct {
					Field        string `json:"field"`
					MediaType    string `json:"media_type"`
					Label        string `json:"label"`
					ChooserField string `json:"chooser_field"`
				} `json:"requester_options"`
			} `json:"request_router"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &raw); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	options := raw.Capabilities[0].RequestRouter.RequesterOptions
	// The storage group select is not among them: while storage groups are
	// on they decide the folder, and nobody picks one per request.
	if len(options) != 4 {
		t.Fatalf("requester_options: want 4, got %d", len(options))
	}
	for _, o := range options {
		wantType := "movie"
		if strings.HasPrefix(o.Field, "sonarr_") {
			wantType = "series"
		}
		if !slices.Contains(destination, o.Field) || strings.HasSuffix(o.Field, "_server_id") || o.MediaType != wantType {
			t.Fatalf("requester option %+v: want a %s profile or folder field", o, wantType)
		}
		if !fields[o.ChooserField] || !strings.HasSuffix(o.ChooserField, "_chosen_by") {
			t.Fatalf("requester option %+v: chooser_field is not a form select", o)
		}
		if o.Label == "" || len(o.Label) > 24 {
			t.Fatalf("requester option %+v: label must be 1 to 24 characters", o)
		}
	}

}

// The storage group form: one switch reveals the group sections and swaps
// each root folder select for a group select.
func TestStorageGroupForm(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	form := m.GetCapabilities()[0].GetConfigSchema()[0].GetAdminForm()
	shownWhen := map[string]string{}
	controls := map[string]string{}
	for _, f := range form.GetFields() {
		controls[f.GetKey()] = f.GetControl().String()
		for _, c := range f.GetShowWhen() {
			if c.GetField() == "storage_groups_enabled" {
				shownWhen[f.GetKey()] = strings.Join(c.GetEquals(), ",")
			}
		}
	}
	if controls["storage_groups_enabled"] != "ADMIN_FORM_CONTROL_SWITCH" {
		t.Fatalf("storage_groups_enabled: want a switch, got %q", controls["storage_groups_enabled"])
	}
	// Silo's rule editor lists every select and multi-select a server's form
	// shows, and checks a field's own show_when only. So each field carries
	// the condition, not just its section.
	// "In order" fills a group's folders by their place in the list, so the
	// folder lists say that their order matters.
	var rawForm struct {
		Capabilities []struct {
			ConfigSchema []struct {
				AdminForm struct {
					Fields []struct {
						Key     string `json:"key"`
						Ordered bool   `json:"ordered"`
					} `json:"fields"`
				} `json:"admin_form"`
			} `json:"config_schema"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &rawForm); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	for _, f := range rawForm.Capabilities[0].ConfigSchema[0].AdminForm.Fields {
		if strings.HasSuffix(f.Key, "_folders") != f.Ordered {
			t.Fatalf("%s: ordered=%v, want it set on the group folder lists only", f.Key, f.Ordered)
		}
	}
	on := []string{"storage_min_free_gb", "radarr_storage_group", "sonarr_storage_group"}
	for slot := 1; slot <= 8; slot++ {
		for _, suffix := range []string{"name", "radarr_folders", "sonarr_folders", "mode"} {
			on = append(on, "storage_group_"+string(rune('0'+slot))+"_"+suffix)
		}
	}
	for _, key := range on {
		if controls[key] == "" || shownWhen[key] != "true" {
			t.Fatalf("%s: want a field shown only while storage groups are on (control %q, shown when %q)", key, controls[key], shownWhen[key])
		}
	}
	for _, key := range []string{"radarr_root_folder", "sonarr_root_folder", "radarr_folder_chosen_by", "sonarr_folder_chosen_by"} {
		if shownWhen[key] != "false" {
			t.Fatalf("%s: want it hidden while storage groups are on", key)
		}
	}
	// Every field sits in a section, and the group sections appear with the switch.
	inSection := map[string]bool{}
	groupSections := 0
	for _, s := range form.GetSections() {
		for _, key := range s.GetFieldKeys() {
			inSection[key] = true
		}
		if strings.HasPrefix(s.GetKey(), "storage_group_") {
			if len(s.GetShowWhen()) != 1 {
				t.Fatalf("section %s: want it shown only while storage groups are on", s.GetKey())
			}
			groupSections++
		}
	}
	if groupSections != 8 {
		t.Fatalf("want 8 storage group sections, got %d", groupSections)
	}
	for key := range controls {
		if !inSection[key] {
			t.Fatalf("%s is in no section", key)
		}
	}
}
