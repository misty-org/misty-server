package collab

import "testing"

func TestOptions(t *testing.T) {
	requested, err := ParseRequested([]byte(`{"file":"drv_a"}`))
	if err != nil {
		t.Fatal(err)
	}
	options := requested.New()
	if options.File != "drv_a" || options.Text != "text" || !options.GC || options.History {
		t.Fatalf("defaults %+v", options)
	}
	whole, _ := ParseRequested([]byte(`{"file":"drv_a","text":null,"gc":false}`))
	if got := whole.New(); got.Text != "" || got.GC || !got.WholeDocument() {
		t.Fatalf("whole document %+v", got)
	}
	if whole.Fits(options) {
		t.Fatal("a whole-document open fits a text room")
	}
	empty, _ := ParseRequested(nil)
	if !empty.Fits(options) {
		t.Fatal("leaving options out does not take the room's")
	}
	if stored := ParseStoredOptions(options.JSON()); stored != options {
		t.Fatalf("round trip %+v", stored)
	}
	for _, bad := range []string{`[]`, `{"file":""}`, `{"history":"yes"}`, `{"text":""}`} {
		if _, err := ParseRequested([]byte(bad)); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
