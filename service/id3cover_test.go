package service

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func syncsafeBytes(n int) []byte {
	return []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}

// apic makes the body of an APIC frame with a Latin-1 description.
func apic(mime string, pictureType byte, description string, data []byte) []byte {
	b := []byte{0}
	b = append(b, mime...)
	b = append(b, 0, pictureType)
	b = append(b, description...)
	b = append(b, 0)
	return append(b, data...)
}

// frame makes a v2.3 or v2.4 frame.
func frame(version byte, id string, body []byte) []byte {
	b := []byte(id)
	if version == 4 {
		b = append(b, syncsafeBytes(len(body))...)
	} else {
		b = binary.BigEndian.AppendUint32(b, uint32(len(body)))
	}
	b = append(b, 0, 0)
	return append(b, body...)
}

// tag makes an ID3v2 tag with the frames and 20 bytes of padding, and some audio after it.
func tag(version, flags byte, frames ...[]byte) []byte {
	body := bytes.Join(frames, nil)
	body = append(body, make([]byte, 20)...)
	b := []byte{'I', 'D', '3', version, 0, flags}
	b = append(b, syncsafeBytes(len(body))...)
	b = append(b, body...)
	return append(b, 0xFF, 0xFB, 0x90, 0x00) // the start of an mp3 frame
}

func TestID3FrontCover(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'j', 'p', 'g'}
	png := []byte{0x89, 'P', 'N', 'G'}

	cases := map[string]struct {
		tag      []byte
		wantMIME string
		wantData []byte
	}{
		"v2.4 front cover": {
			tag(4, 0, frame(4, "TIT2", []byte{0, 'T'}), frame(4, "APIC", apic("image/jpeg", 3, "", jpeg))),
			"image/jpeg", jpeg,
		},
		"v2.3 picks the front cover": {
			tag(3, 0, frame(3, "APIC", apic("image/png", 0, "other", png)), frame(3, "APIC", apic("image/jpg", 3, "cover", jpeg))),
			"image/jpeg", jpeg,
		},
		"v2.3 without a front cover picks the first picture": {
			tag(3, 0, frame(3, "APIC", apic("image/png", 4, "back", png))),
			"image/png", png,
		},
		"UTF-16 description": {
			tag(3, 0, frame(3, "APIC", append([]byte{1, 'i', 'm', 'a', 'g', 'e', '/', 'p', 'n', 'g', 0, 3, 0xFF, 0xFE, 'a', 0, 0, 0}, png...))),
			"image/png", png,
		},
		"v2.2 PIC": {
			func() []byte {
				body := append([]byte{0, 'J', 'P', 'G', 3, 0}, jpeg...)
				f := append([]byte{'P', 'I', 'C', 0, 0, byte(len(body))}, body...)
				return tag(2, 0, f)
			}(),
			"image/jpeg", jpeg,
		},
		"v2.4 frame with a data length indicator": {
			func() []byte {
				body := append([]byte{0, 0, 0, 9}, apic("image/png", 3, "", png)...)
				f := frame(4, "APIC", body)
				f[9] = 0x01
				return tag(4, 0, f)
			}(),
			"image/png", png,
		},
	}
	for name, tc := range cases {
		pic, ok := id3FrontCover(tc.tag)
		if !ok {
			t.Errorf("%s: no picture", name)
			continue
		}
		if pic.MIME != tc.wantMIME || !bytes.Equal(pic.Data, tc.wantData) {
			t.Errorf("%s: picture = %s %x, want %s %x", name, pic.MIME, pic.Data, tc.wantMIME, tc.wantData)
		}
	}
}

func TestID3FrontCover__NoPicture(t *testing.T) {
	cases := map[string][]byte{
		"no tag":       {0xFF, 0xFB, 0x90, 0x00},
		"no picture":   tag(4, 0, frame(4, "TIT2", []byte{0, 'T'})),
		"a link":       tag(3, 0, frame(3, "APIC", apic("-->", 3, "", []byte("http://x")))),
		"cut tag":      tag(4, 0, frame(4, "APIC", apic("image/jpeg", 3, "", []byte{1, 2, 3})))[:20],
		"broken frame": tag(3, 0, []byte{'A', 'P', 'I', 'C', 0, 0, 0xFF, 0xFF, 0, 0}),
	}
	for name, data := range cases {
		if pic, ok := id3FrontCover(data); ok {
			t.Errorf("%s: picture = %+v, want none", name, pic)
		}
	}
}

func TestID3TagLen(t *testing.T) {
	data := tag(4, 0, frame(4, "TIT2", []byte{0, 'T'}))
	n, ok := id3TagLen(data[:10])
	if !ok || n != len(data)-4 {
		t.Errorf("id3TagLen() = %d, %v; want %d", n, ok, len(data)-4)
	}
	if _, ok := id3TagLen([]byte("RIFF....WAVE")); ok {
		t.Error("id3TagLen() of a file without a tag = true")
	}
}

func TestRemoveUnsync(t *testing.T) {
	if got := removeUnsync([]byte{1, 0xFF, 0x00, 0xE0, 0xFF, 0x00, 0x00}); !bytes.Equal(got, []byte{1, 0xFF, 0xE0, 0xFF, 0x00}) {
		t.Errorf("removeUnsync() = %x", got)
	}
}
