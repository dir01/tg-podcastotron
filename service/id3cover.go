package service

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// id3HeaderLen is the length of the ID3v2 tag header at the start of an mp3 file.
const id3HeaderLen = 10

// id3TagLen returns the length of the ID3v2 tag that starts the data, with its
// header. The data needs only the 10 bytes of the header. It returns false if the
// data does not start with an ID3v2 tag.
func id3TagLen(header []byte) (int, bool) {
	if len(header) < id3HeaderLen || string(header[:3]) != "ID3" || header[3] < 2 || header[3] > 4 {
		return 0, false
	}
	size, ok := syncsafe(header[6:10])
	if !ok {
		return 0, false
	}
	return id3HeaderLen + size, true
}

// id3Picture is a picture from an ID3v2 tag.
type id3Picture struct {
	MIME string // "image/jpeg" or "image/png"
	Data []byte
}

// id3FrontCover returns the picture of the ID3v2 tag at the start of tag: the
// front cover if there is one, else the first picture. tag must have all of the tag.
func id3FrontCover(tag []byte) (*id3Picture, bool) {
	tagLen, ok := id3TagLen(tag)
	if !ok || len(tag) < tagLen {
		return nil, false
	}
	version, flags := tag[3], tag[5]
	body := tag[id3HeaderLen:tagLen]
	// In v2.2 and v2.3, unsynchronisation applies to all of the tag.
	if flags&0x80 != 0 && version < 4 {
		body = removeUnsync(body)
	}
	if flags&0x40 != 0 && version >= 3 { // extended header
		skip, ok := extendedHeaderLen(body, version)
		if !ok {
			return nil, false
		}
		body = body[skip:]
	}

	var first *id3Picture
	for len(body) > 0 {
		f, rest, ok := nextID3Frame(body, version)
		if !ok {
			break
		}
		body = rest
		if f.id != "APIC" && f.id != "PIC" {
			continue
		}
		pic, pictureType, ok := parsePictureFrame(f.data, version)
		if !ok {
			continue
		}
		if pictureType == 3 { // front cover
			return pic, true
		}
		if first == nil {
			first = pic
		}
	}
	return first, first != nil
}

type id3Frame struct {
	id   string
	data []byte
}

// nextID3Frame reads the frame at the start of body. It returns false at the
// padding or at the end of the frames.
func nextID3Frame(body []byte, version byte) (id3Frame, []byte, bool) {
	idLen, headerLen := 4, 10
	if version == 2 {
		idLen, headerLen = 3, 6
	}
	if len(body) < headerLen || body[0] == 0 {
		return id3Frame{}, nil, false
	}
	id := string(body[:idLen])
	var size int
	switch version {
	case 2:
		size = int(body[3])<<16 | int(body[4])<<8 | int(body[5])
	case 3:
		size = int(binary.BigEndian.Uint32(body[4:8]))
	default:
		s, ok := syncsafe(body[4:8])
		if !ok {
			return id3Frame{}, nil, false
		}
		size = s
	}
	if size < 0 || headerLen+size > len(body) {
		return id3Frame{}, nil, false
	}
	data := body[headerLen : headerLen+size]
	rest := body[headerLen+size:]

	if version >= 3 {
		format := body[9]
		compressed, encrypted := format&0x80 != 0, format&0x40 != 0
		if version == 4 {
			compressed, encrypted = format&0x08 != 0, format&0x04 != 0
		}
		if compressed || encrypted {
			return id3Frame{id: "skip"}, rest, true
		}
		if version == 4 && format&0x01 != 0 { // data length indicator
			if len(data) < 4 {
				return id3Frame{id: "skip"}, rest, true
			}
			data = data[4:]
		}
		if version == 4 && format&0x02 != 0 { // unsynchronisation of this frame
			data = removeUnsync(data)
		}
	}
	return id3Frame{id: id, data: data}, rest, true
}

// parsePictureFrame reads an APIC (v2.3, v2.4) or a PIC (v2.2) frame.
func parsePictureFrame(data []byte, version byte) (*id3Picture, byte, bool) {
	if len(data) < 2 {
		return nil, 0, false
	}
	encoding := data[0]
	data = data[1:]

	var mime string
	if version == 2 {
		if len(data) < 3 {
			return nil, 0, false
		}
		mime = "image/" + strings.ToLower(string(data[:3]))
		data = data[3:]
	} else {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, 0, false
		}
		mime = strings.ToLower(string(data[:end]))
		data = data[end+1:]
	}
	switch mime {
	case "image/jpeg", "image/jpg":
		mime = "image/jpeg"
	case "image/png":
	default:
		return nil, 0, false // for example "-->", a link to a picture
	}

	if len(data) < 1 {
		return nil, 0, false
	}
	pictureType := data[0]
	data = data[1:]

	// Skip the description. UTF-16 text (encodings 1 and 2) ends with two zero bytes.
	if encoding == 1 || encoding == 2 {
		end := -1
		for i := 0; i+1 < len(data); i += 2 {
			if data[i] == 0 && data[i+1] == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, 0, false
		}
		data = data[end+2:]
	} else {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, 0, false
		}
		data = data[end+1:]
	}
	if len(data) == 0 {
		return nil, 0, false
	}
	return &id3Picture{MIME: mime, Data: data}, pictureType, true
}

func extendedHeaderLen(body []byte, version byte) (int, bool) {
	if len(body) < 4 {
		return 0, false
	}
	if version == 3 {
		// The size does not include the 4 bytes of the size.
		n := int(binary.BigEndian.Uint32(body[:4])) + 4
		return n, n <= len(body)
	}
	n, ok := syncsafe(body[:4])
	return n, ok && n <= len(body)
}

// syncsafe decodes a 4-byte ID3 "syncsafe" integer: 7 bits from each byte.
func syncsafe(b []byte) (int, bool) {
	n := 0
	for _, c := range b[:4] {
		if c&0x80 != 0 {
			return 0, false
		}
		n = n<<7 | int(c)
	}
	return n, true
}

// removeUnsync reverses the ID3 unsynchronisation: each 0xFF 0x00 becomes 0xFF.
func removeUnsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0x00 {
			i++
		}
	}
	return out
}
