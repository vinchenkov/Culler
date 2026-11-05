package verbs

import (
	"reflect"
	"testing"
)

func TestProjectImageDigestsListsEachSelectedImageAndBaseOnce(t *testing.T) {
	db := environmentFixture(t)
	digests := func() []string {
		t.Helper()
		value, err := ProjectImageDigests(db)
		if err != nil {
			t.Fatal(err)
		}
		return value.(map[string]any)["digests"].([]string)
	}
	if got := digests(); len(got) != 0 {
		t.Fatalf("an unselected environment listed %v", got)
	}
	dvExec(t, db, `UPDATE project_environments SET selected_base_digest=? WHERE project_id='ws-test'`, environmentTestBase)
	if got := digests(); !reflect.DeepEqual(got, []string{environmentTestBase}) {
		t.Fatalf("selected base: %v", got)
	}
	dvExec(t, db, `UPDATE project_environments SET selected_image_digest=? WHERE project_id='ws-test'`, environmentTestBase)
	if got := digests(); !reflect.DeepEqual(got, []string{environmentTestBase}) {
		t.Fatalf("the same image and base listed twice: %v", got)
	}
	dvExec(t, db, `UPDATE project_environments SET selected_image_digest=? WHERE project_id='ws-test'`, environmentTestImage)
	if got := digests(); !reflect.DeepEqual(got, []string{environmentTestBase, environmentTestImage}) {
		t.Fatalf("selected image and base: %v", got)
	}
	dvExec(t, db, `UPDATE projects SET status='archived' WHERE id='ws-test'`)
	if got := digests(); !reflect.DeepEqual(got, []string{environmentTestBase, environmentTestImage}) {
		t.Fatalf("an archived Project lost its images: %v", got)
	}
}
