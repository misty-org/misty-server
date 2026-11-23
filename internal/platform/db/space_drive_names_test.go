package db

import "testing"

func TestDriveHiddenRunes(t *testing.T) {
	for _, r := range []rune{0x202A, 0x202E, 0x2066, 0x2069, 0x200B, 0x200D, 0x2060, 0xFEFF} {
		if !DriveHiddenRune(r) {
			t.Fatalf("%U should be refused", r)
		}
	}
	for _, r := range []rune{'a', 'ß', 0x2070, 0x200E} {
		if DriveHiddenRune(r) {
			t.Fatalf("%U should be allowed", r)
		}
	}
}
