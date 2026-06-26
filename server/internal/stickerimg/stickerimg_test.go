package stickerimg

import "testing"

func TestReadKnownAndUnknown(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no embedded sticker images")
	}
	if data, ok := Read(names[0]); !ok || len(data) == 0 {
		t.Errorf("Read(%q) = ok %v, %d bytes; want ok with bytes", names[0], ok, len(data))
	}
	// Unknown names and traversal attempts must return ok=false.
	for _, bad := range []string{"nope.png", "", "/", "../stickerimg.go", "../../stickers/catalog.json", "files/x"} {
		if _, ok := Read(bad); ok {
			t.Errorf("Read(%q) = ok true; want false", bad)
		}
	}
}
