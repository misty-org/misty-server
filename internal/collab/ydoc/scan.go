package ydoc

import (
	"errors"
	"sort"

	"github.com/reearth/ygo/encoding"
)

type Kind string

const (
	KindText  Kind = "text"
	KindMap   Kind = "map"
	KindArray Kind = "array"
	KindXML   Kind = "xml"
)

type Root struct {
	Name string
	Kind Kind
}

var ErrMalformed = errors.New("ydoc: malformed update")

const (
	contentDeleted = 1
	contentJSON    = 2
	contentBinary  = 3
	contentString  = 4
	contentEmbed   = 5
	contentFormat  = 6
	contentType    = 7
	contentAny     = 8
	contentDoc     = 9

	typeXMLElement = 3
	typeXMLHook    = 5
	typeXMLText    = 6

	maxScanStructs = 1 << 19
)

type scanID struct {
	client uint64
	clock  uint64
}

type scanStruct struct {
	id          scanID
	length      uint64
	item        bool
	content     uint8
	typeRef     uint64
	hasOrigin   bool
	origin      scanID
	hasRight    bool
	right       scanID
	explicit    bool
	root        string
	hasRoot     bool
	mapEntry    bool
	resolved    int8
	resRoot     string
	resIsRoot   bool
	contentLeft int
}

type scanned struct {
	clients map[uint64][]*scanStruct
	deletes map[uint64][][2]uint64
}

func scanUpdate(update []byte) (*scanned, error) {
	dec := encoding.NewDecoder(update)
	out := &scanned{clients: map[uint64][]*scanStruct{}, deletes: map[uint64][][2]uint64{}}
	numClients, err := dec.ReadVarUint()
	if err != nil {
		return nil, ErrMalformed
	}
	total := 0
	for c := uint64(0); c < numClients; c++ {
		numStructs, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		client, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		clock, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		for i := uint64(0); i < numStructs; i++ {
			total++
			if total > maxScanStructs {
				return nil, ErrMalformed
			}
			s, err := scanOne(dec, client, clock)
			if err != nil {
				return nil, err
			}
			out.clients[client] = append(out.clients[client], s)
			clock += s.length
		}
	}
	numDeleteClients, err := dec.ReadVarUint()
	if err != nil {
		return nil, ErrMalformed
	}
	for c := uint64(0); c < numDeleteClients; c++ {
		client, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		ranges, err := dec.ReadVarUint()
		if err != nil || ranges > uint64(dec.Remaining()) {
			return nil, ErrMalformed
		}
		for r := uint64(0); r < ranges; r++ {
			start, err := dec.ReadVarUint()
			if err != nil {
				return nil, ErrMalformed
			}
			length, err := dec.ReadVarUint()
			if err != nil {
				return nil, ErrMalformed
			}
			out.deletes[client] = append(out.deletes[client], [2]uint64{start, length})
		}
	}
	for client := range out.clients {
		structs := out.clients[client]
		sort.Slice(structs, func(i, j int) bool { return structs[i].id.clock < structs[j].id.clock })
	}
	return out, nil
}

func scanOne(dec *encoding.Decoder, client, clock uint64) (*scanStruct, error) {
	info, err := dec.ReadUint8()
	if err != nil {
		return nil, ErrMalformed
	}
	s := &scanStruct{id: scanID{client, clock}}
	switch info & 0x1f {
	case 0, 10:
		length, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		s.length = length
		return s, nil
	}
	s.item = true
	if info&0x80 != 0 {
		if s.origin, err = readID(dec); err != nil {
			return nil, err
		}
		s.hasOrigin = true
	}
	if info&0x40 != 0 {
		if s.right, err = readID(dec); err != nil {
			return nil, err
		}
		s.hasRight = true
	}
	if info&0xc0 == 0 {
		s.explicit = true
		parentInfo, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		if parentInfo == 1 {
			name, err := dec.ReadVarString()
			if err != nil {
				return nil, ErrMalformed
			}
			s.root, s.hasRoot = name, true
		} else if _, err := readID(dec); err != nil {
			return nil, err
		}
		if info&0x20 != 0 {
			if _, err := dec.ReadVarString(); err != nil {
				return nil, ErrMalformed
			}
			s.mapEntry = true
		}
	}
	s.content = info & 0x1f
	s.contentLeft = dec.Remaining()
	length, typeRef, err := skipContent(dec, s.content)
	if err != nil {
		return nil, err
	}
	s.length, s.typeRef = length, typeRef
	return s, nil
}

func readID(dec *encoding.Decoder) (scanID, error) {
	client, err := dec.ReadVarUint()
	if err != nil {
		return scanID{}, ErrMalformed
	}
	clock, err := dec.ReadVarUint()
	if err != nil {
		return scanID{}, ErrMalformed
	}
	return scanID{client, clock}, nil
}

func skipContent(dec *encoding.Decoder, ref uint8) (uint64, uint64, error) {
	switch ref {
	case contentDeleted:
		length, err := dec.ReadVarUint()
		return length, 0, malformed(err)
	case contentJSON:
		count, err := dec.ReadVarUint()
		if err != nil || count > uint64(dec.Remaining())+1 {
			return 0, 0, ErrMalformed
		}
		for i := uint64(0); i < count; i++ {
			if _, err := dec.ReadVarString(); err != nil {
				return 0, 0, ErrMalformed
			}
		}
		return count, 0, nil
	case contentBinary:
		_, err := dec.ReadVarBytes()
		return 1, 0, malformed(err)
	case contentString:
		text, err := dec.ReadVarString()
		return utf16Length(text), 0, malformed(err)
	case contentEmbed:
		_, err := dec.ReadVarString()
		return 1, 0, malformed(err)
	case contentFormat:
		if _, err := dec.ReadVarString(); err != nil {
			return 0, 0, ErrMalformed
		}
		_, err := dec.ReadVarString()
		return 1, 0, malformed(err)
	case contentType:
		typeRef, err := dec.ReadVarUint()
		if err != nil {
			return 0, 0, ErrMalformed
		}
		if typeRef == typeXMLElement || typeRef == typeXMLHook {
			if _, err := dec.ReadVarString(); err != nil {
				return 0, 0, ErrMalformed
			}
		}
		return 1, typeRef, nil
	case contentAny:
		count, err := dec.ReadVarUint()
		if err != nil || count > uint64(dec.Remaining())+1 {
			return 0, 0, ErrMalformed
		}
		for i := uint64(0); i < count; i++ {
			if _, err := dec.ReadAny(); err != nil {
				return 0, 0, ErrMalformed
			}
		}
		return count, 0, nil
	case contentDoc:
		if _, err := dec.ReadVarString(); err != nil {
			return 0, 0, ErrMalformed
		}
		_, err := dec.ReadAny()
		return 1, 0, malformed(err)
	}
	return 0, 0, ErrMalformed
}

func malformed(err error) error {
	if err != nil {
		return ErrMalformed
	}
	return nil
}

func utf16Length(text string) uint64 {
	var n uint64
	for _, r := range text {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func (s *scanned) find(id scanID) *scanStruct {
	structs := s.clients[id.client]
	i := sort.Search(len(structs), func(i int) bool { return structs[i].id.clock+structs[i].length > id.clock })
	if i < len(structs) && structs[i].id.clock <= id.clock {
		return structs[i]
	}
	return nil
}

func (s *scanned) rootOf(item *scanStruct, depth int) (string, bool) {
	if item == nil || !item.item || depth > 10000 {
		return "", false
	}
	if item.resolved != 0 {
		return item.resRoot, item.resIsRoot
	}
	item.resolved = -1
	var name string
	var isRoot bool
	switch {
	case item.explicit:
		name, isRoot = item.root, item.hasRoot
	case item.hasOrigin:
		name, isRoot = s.rootOf(s.find(item.origin), depth+1)
		if !isRoot && item.hasRight {
			name, isRoot = s.rootOf(s.find(item.right), depth+1)
		}
	case item.hasRight:
		name, isRoot = s.rootOf(s.find(item.right), depth+1)
	}
	if !item.explicit && item.hasOrigin {
		if origin := s.find(item.origin); origin != nil && origin.item {
			item.mapEntry = origin.mapEntry
		}
	}
	item.resolved, item.resRoot, item.resIsRoot = 1, name, isRoot
	return name, isRoot
}

func (s *scanned) deleted(item *scanStruct) bool {
	if item.content == contentDeleted {
		return true
	}
	start, end := item.id.clock, item.id.clock+item.length
	covered := start
	for _, r := range s.deletes[item.id.client] {
		if r[0] <= covered && r[0]+r[1] > covered {
			covered = r[0] + r[1]
			if covered >= end {
				return true
			}
		}
	}
	return covered >= end
}

func classifyRoots(update []byte) ([]Root, error) {
	s, err := scanUpdate(update)
	if err != nil {
		return nil, err
	}
	type shape struct{ mapLive, textLive, xmlLive, listLive, seen bool }
	shapes := map[string]*shape{}
	clients := make([]uint64, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	for _, client := range clients {
		for _, item := range s.clients[client] {
			name, isRoot := s.rootOf(item, 0)
			if !isRoot {
				continue
			}
			sh := shapes[name]
			if sh == nil {
				sh = &shape{}
				shapes[name] = sh
			}
			sh.seen = true
			if s.deleted(item) {
				continue
			}
			switch {
			case item.mapEntry:
				sh.mapLive = true
			case item.content == contentString || item.content == contentEmbed || item.content == contentFormat:
				sh.textLive = true
			case item.content == contentType && (item.typeRef == typeXMLElement || item.typeRef == typeXMLHook || item.typeRef == typeXMLText):
				sh.xmlLive = true
			default:
				sh.listLive = true
			}
		}
	}
	names := make([]string, 0, len(shapes))
	for name := range shapes {
		names = append(names, name)
	}
	sort.Strings(names)
	roots := make([]Root, 0, len(names))
	for _, name := range names {
		sh := shapes[name]
		var kind Kind
		switch {
		case sh.mapLive:
			kind = KindMap
		case sh.textLive:
			kind = KindText
		case sh.xmlLive:
			kind = KindXML
		case sh.listLive:
			kind = KindArray
		default:
			continue
		}
		roots = append(roots, Root{Name: name, Kind: kind})
	}
	return roots, nil
}

func updateClients(update []byte) ([]uint64, error) {
	dec := encoding.NewDecoder(update)
	numClients, err := dec.ReadVarUint()
	if err != nil {
		return nil, ErrMalformed
	}
	clients := []uint64{}
	for c := uint64(0); c < numClients; c++ {
		numStructs, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		client, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrMalformed
		}
		if _, err := dec.ReadVarUint(); err != nil {
			return nil, ErrMalformed
		}
		clients = append(clients, client)
		for i := uint64(0); i < numStructs; i++ {
			if _, err := scanOne(dec, client, 0); err != nil {
				return nil, err
			}
		}
	}
	return clients, nil
}
