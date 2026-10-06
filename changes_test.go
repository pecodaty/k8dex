package k8dex

import "testing"

// The diff between two catalogs lists removals first and matches the
// published change notes for a known removal.
func TestAPIChangesBetweenCatalogs(t *testing.T) {
	changes, err := APIChanges("v1.33", "v1.34")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range changes {
		if c.Group == "admissionregistration.k8s.io" && c.Version == "v1beta1" && c.Kind == "ValidatingAdmissionPolicy" {
			found = c.Change == "removed"
		}
	}
	if !found || changes[0].Change != "removed" {
		t.Fatalf("changes: %+v", changes[:min(len(changes), 5)])
	}
	if _, err := APIChanges("v1.33", "v1.99"); err == nil {
		t.Fatal("unknown version accepted")
	}
}
