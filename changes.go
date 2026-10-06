package k8dex

import (
	"cmp"
	"slices"
)

// APIChange is one group/version/kind present in one catalog and not the other.
type APIChange struct {
	Group   string `json:"group,omitempty"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	// Change is "added" when only the newer version serves it and "removed"
	// when only the older one does.
	Change string `json:"change"`
}

// APIChanges lists the kinds served by exactly one of two supported
// Kubernetes versions, removed ones first. It performs no network calls.
func APIChanges(from, to string) ([]APIChange, error) {
	older, err := CatalogIndex(Options{KubernetesVersion: from})
	if err != nil {
		return nil, err
	}
	newer, err := CatalogIndex(Options{KubernetesVersion: to})
	if err != nil {
		return nil, err
	}
	key := func(e KindEntry) string { return e.Group + "/" + e.Version + "/" + e.Kind }
	inOld, inNew := map[string]KindEntry{}, map[string]KindEntry{}
	for _, e := range older {
		inOld[key(e)] = e
	}
	for _, e := range newer {
		inNew[key(e)] = e
	}
	out := []APIChange{}
	for k, e := range inOld {
		if _, ok := inNew[k]; !ok {
			out = append(out, APIChange{Group: e.Group, Version: e.Version, Kind: e.Kind, Change: "removed"})
		}
	}
	for k, e := range inNew {
		if _, ok := inOld[k]; !ok {
			out = append(out, APIChange{Group: e.Group, Version: e.Version, Kind: e.Kind, Change: "added"})
		}
	}
	slices.SortFunc(out, func(a, b APIChange) int {
		return cmp.Or(cmp.Compare(b.Change, a.Change), cmp.Compare(a.Group, b.Group), cmp.Compare(a.Version, b.Version), cmp.Compare(a.Kind, b.Kind))
	})
	return out, nil
}
