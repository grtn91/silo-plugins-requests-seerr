package router

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-seerr/internal/seerr"
)

// Storage groups let the plugin choose a request's root folder by free
// space. A group is a named list of root folders with a rule for choosing
// among them. Groups are tried in slot order, and a group whose folders are
// all full hands over to the next. While storage groups are on they replace
// the root folder selects.
const (
	keyStorageEnabled     = "storage_groups_enabled"
	keyStorageMinFree     = "storage_min_free_gb"
	keyRadarrStorageGroup = "radarr_storage_group"
	keySonarrStorageGroup = "sonarr_storage_group"

	// storageGroupSlots is how many groups a connection can have. A group is
	// a fixed set of config keys, so the form declares every slot and shows
	// the ones in use, with a button that reveals the next. A slot without
	// folders is unused.
	storageGroupSlots = 8

	// storageAuto is the group select value for "start at the first group".
	storageAuto = "auto"

	storageModeInOrder    = "in_order"   // first listed folder with room
	storageModeDistribute = "distribute" // the folder with the most room

	// defaultStorageMinFreeGB applies when the field is unset.
	defaultStorageMinFreeGB = 50

	bytesPerGB = 1 << 30
)

// groupKey is a group slot's config key, such as storage_group_2_mode.
func groupKey(slot int, suffix string) string {
	return fmt.Sprintf("storage_group_%d_%s", slot, suffix)
}

// groupFoldersKey is the config key of a group's folders on one service kind.
func groupFoldersKey(slot int, kind string) string {
	return groupKey(slot, kind+"_folders")
}

// storageGroup is one configured group slot.
type storageGroup struct {
	Slot       int
	Name       string
	Distribute bool
	// Folders holds the group's root folder paths per service kind, in the
	// order they were picked.
	Folders map[string][]string
}

// label is the group's name, or "Group N" when it has none.
func (g storageGroup) label() string {
	if g.Name != "" {
		return g.Name
	}
	return fmt.Sprintf("Group %d", g.Slot)
}

// storageConfig is a connection's storage group settings.
type storageConfig struct {
	Enabled bool
	// MinFreeBytes is the free space below which a folder counts as full.
	MinFreeBytes float64
	Groups       []storageGroup
	// Errs holds values that could not be read, keyed by config key.
	Errs map[string]string
}

// storageFromConfig reads the storage group settings from a connection's
// config. The groups are read even when storage groups are off, so the form
// can list them.
func storageFromConfig(m map[string]any) storageConfig {
	st := storageConfig{MinFreeBytes: defaultStorageMinFreeGB * bytesPerGB, Errs: map[string]string{}}
	st.Enabled, _ = m[keyStorageEnabled].(bool)
	if gb, ok, err := parseGB(m[keyStorageMinFree]); err != nil {
		st.Errs[keyStorageMinFree] = fmt.Sprintf("%s: %v", keyStorageMinFree, err)
	} else if ok {
		st.MinFreeBytes = gb * bytesPerGB
	}
	for slot := 1; slot <= storageGroupSlots; slot++ {
		g := storageGroup{Slot: slot, Folders: map[string][]string{}}
		if name, ok := m[groupKey(slot, "name")].(string); ok {
			g.Name = strings.TrimSpace(name)
		}
		if mode, ok := m[groupKey(slot, "mode")].(string); ok {
			g.Distribute = strings.TrimSpace(mode) == storageModeDistribute
		}
		for _, f := range services {
			g.Folders[f.Kind] = stringList(m[groupFoldersKey(slot, f.Kind)])
		}
		st.Groups = append(st.Groups, g)
	}
	// A group is picked by its name, so two groups cannot share one.
	named := map[string]int{}
	for _, g := range st.Groups {
		if g.Name == "" {
			continue
		}
		if first, taken := named[g.Name]; taken {
			key := groupKey(g.Slot, "name")
			st.Errs[key] = fmt.Sprintf("%s: storage groups %d and %d are both named %q", key, first, g.Slot, g.Name)
			continue
		}
		named[g.Name] = g.Slot
	}
	return st
}

// parseGB reads a size in GB. ok is false when the value is unset.
func parseGB(v any) (gb float64, ok bool, err error) {
	switch x := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		gb = x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false, nil
		}
		if gb, err = strconv.ParseFloat(s, 64); err != nil {
			return 0, false, fmt.Errorf("%q is not a number of GB", x)
		}
	default:
		return 0, false, fmt.Errorf("%v is not a number of GB", x)
	}
	if gb < 0 || math.IsNaN(gb) || math.IsInf(gb, 0) {
		return 0, false, fmt.Errorf("%v is not a number of GB of 0 or more", v)
	}
	return gb, true, nil
}

// stringList reads a multi-select value: the non-empty strings of a list.
func stringList(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// manages reports whether the storage groups choose the folder for kind: they
// are on and at least one group lists a folder of that kind. A kind no group
// covers keeps Seerr's own folder.
func (st storageConfig) manages(kind string) bool {
	if !st.Enabled {
		return false
	}
	for _, g := range st.Groups {
		if len(g.Folders[kind]) > 0 {
			return true
		}
	}
	return false
}

// pickValue is the group's value in a group select: its name, so a pick
// follows the group when the groups are put in another order, or its slot
// number while it has no name.
func (g storageGroup) pickValue() string {
	if g.Name != "" {
		return g.Name
	}
	return itoa(g.Slot)
}

// startSlot reads a group select: 0 for "auto", or the slot to start at. A
// group is named by its pickValue; a slot number is also read for a named
// group, as earlier versions stored it. The group must have folders of kind.
func (st storageConfig) startSlot(v any, kind string) (int, error) {
	var s string
	switch x := v.(type) {
	case nil:
		return 0, nil
	case string:
		s = strings.TrimSpace(x)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return 0, fmt.Errorf("%v is not a storage group", x)
	}
	if s == "" || strings.EqualFold(s, storageAuto) {
		return 0, nil
	}
	slot := 0
	for _, g := range st.Groups {
		if g.Name == s {
			slot = g.Slot
			break
		}
	}
	if slot == 0 {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > storageGroupSlots {
			return 0, fmt.Errorf("%q is not a storage group; it may have been renamed", s)
		}
		slot = n
	}
	if g := st.Groups[slot-1]; len(g.Folders[kind]) == 0 {
		return 0, fmt.Errorf("storage group %q has no %s folders", g.label(), kindLabel(kind))
	}
	return slot, nil
}

// missingFolders lists, per config key, the group folders of kind that the
// server does not have.
func (st storageConfig) missingFolders(kind string, serverName string, folders []seerr.RootFolder) map[string]string {
	out := map[string]string{}
	for _, g := range st.Groups {
		var missing []string
		for _, path := range g.Folders[kind] {
			if !hasRootFolder(folders, path) {
				missing = append(missing, strconv.Quote(path))
			}
		}
		if len(missing) > 0 {
			key := groupFoldersKey(g.Slot, kind)
			out[key] = fmt.Sprintf("%s lists %s, which is not a root folder of Seerr's %s server %q", key, strings.Join(missing, ", "), kindLabel(kind), serverName)
		}
	}
	return out
}

// pickFolder chooses the root folder of kind for one request. Groups are
// tried in slot order, from startSlot when it is set. Within a group only
// folders the server has, with at least MinFreeBytes free, count: the first
// listed one, or the one with the most free space when the group distributes.
// A group without such a folder hands over to the next.
func (st storageConfig) pickFolder(kind string, startSlot int, folders []seerr.RootFolder) (path string, group storageGroup, err error) {
	free := map[string]float64{}
	for _, f := range folders {
		free[f.Path] = f.FreeSpace
	}
	for _, g := range st.Groups {
		if g.Slot < startSlot {
			continue
		}
		best, found := "", false
		for _, p := range g.Folders[kind] {
			space, exists := free[p]
			if !exists || space < st.MinFreeBytes {
				continue
			}
			if !found || (g.Distribute && space > free[best]) {
				best, found = p, true
			}
			if !g.Distribute {
				break
			}
		}
		if found {
			return best, g, nil
		}
	}
	return "", storageGroup{}, fmt.Errorf("no %s folder of the storage groups has %s free; free up space, add a folder to a group, or lower %s", kindLabel(kind), formatGB(st.MinFreeBytes), keyStorageMinFree)
}

// formatGB writes a byte count as whole GB.
func formatGB(bytes float64) string {
	return fmt.Sprintf("%.0f GB", bytes/bytesPerGB)
}

// groupOptions lists the group select's choices for kind: "auto", then every
// group with folders of that kind.
func (st storageConfig) groupOptions(kind string) []*pluginv1.ConfigOption {
	out := []*pluginv1.ConfigOption{{Value: storageAuto, Label: "Automatic (groups in order)"}}
	for _, g := range st.Groups {
		if len(g.Folders[kind]) > 0 {
			out = append(out, &pluginv1.ConfigOption{Value: g.pickValue(), Label: g.label()})
		}
	}
	return out
}
