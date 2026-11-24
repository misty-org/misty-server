package file

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

func (f *File) Joined(s *collab.Session) {
	userID := s.UserID()
	if userID == "" || f.blocking {
		return
	}
	writes := !s.ReadOnly()
	if writes {
		f.noteEditor(userID)
	}
	if writes && (!f.writerIsHere() || f.writerIsUser(userID)) {
		f.setWriter(s)
		f.tellAll()
		return
	}
	f.tell(s)
}

func (f *File) Left(s *collab.Session) {
	delete(f.released, s)
	if f.writer != s {
		return
	}
	if f.restarting {
		f.setWriter(nil)
		return
	}
	f.handOver()
}

func (f *File) Message(s *collab.Session, message wire.OptionMessage, fields map[string]json.RawMessage) {
	if s.ReadOnly() || f.blocking {
		return
	}
	fromWriter := s == f.writer
	_, hasRenamed := fields["renamed"]
	switch {
	case message.Type == "file_mode":
		if rawBool(fields, "content") {
			f.useContentVersions()
		}
		return
	case message.Type == "file_saved" && hasRenamed:
		f.renamed(rawVersion(fields, "renamed"))
		return
	case message.Type == "file_saved":
		fingerprint, fingerprintGiven := rawString(fields, "fingerprint")
		vector, vectorGiven := rawString(fields, "state_vector")
		f.saved(s, validFingerprint(fingerprint, fingerprintGiven), vector, vectorGiven, rawVersion(fields, "version"))
		return
	case message.Type == "file_reload":
		keptCopy := rawBool(fields, "kept")
		fingerprint, given := rawString(fields, "fingerprint")
		keptName := ""
		if keptCopy {
			keptName = nameOf(rawString(fields, "kept_name"))
		}
		go f.reload(s, rawVersion(fields, "version"), keptCopy, validFingerprint(fingerprint, given), keptName)
		return
	case message.Type == "file_resolve":
		f.resolve(s, rawVersion(fields, "version"))
		return
	case message.Type == "file_kept":
		name, nameGiven := rawString(fields, "kept_name")
		fingerprint, given := rawString(fields, "fingerprint")
		f.keptForResolve(s, rawVersion(fields, "version"), nameOf(name, nameGiven), validFingerprint(fingerprint, given))
		return
	case message.Type == "file_outside" && f.isText():
		go f.stopTextSaves(s, rawVersion(fields, "version"))
		return
	}
	if !fromWriter {
		return
	}
	switch message.Type {
	case "writer_ok":
		nonce, ok := rawString(fields, "nonce")
		if ok {
			f.writerAnswered(s, nonce)
		}
	case "file_outside":
		if f.resolving != nil {
			f.endResolve()
		} else {
			for _, other := range f.host.Sessions() {
				if other != s {
					f.send(other, map[string]any{"type": "file_outside"})
				}
			}
		}
	case "file_writer_release":
		f.released[s] = true
		f.handOver()
	}
}

func (f *File) saved(s *collab.Session, fingerprint, vector string, vectorGiven bool, asked int64) {
	fromWriter := s == f.writer
	if fromWriter && (fingerprint != "" || vectorGiven) {
		f.quietSince = time.Now()
		f.checkWriterIfQuiet()
	}
	version := int64(0)
	if fingerprint != "" {
		version = asked
	}
	if version > 0 && f.loaded() && version > f.baseVersion {
		go f.raiseBase(s, version)
	}
	if !fromWriter {
		if fingerprint != "" && f.loaded() && fingerprint == f.fingerprint() {
			f.recordSaved(fingerprint)
		}
		return
	}
	if fingerprint != "" {
		f.recordSaved(fingerprint)
		return
	}
	if !vectorGiven || len(vector) > maxLegacyVector {
		return
	}
	if legacy, ok := f.legacyFingerprint(vector); ok {
		f.recordSaved(legacy)
	}
}

func (f *File) legacyFingerprint(vector string) (string, bool) {
	current, err := f.host.Doc().StateVector()
	if err != nil || vector != base64.StdEncoding.EncodeToString(current) {
		return "", false
	}
	snapshot, err := f.host.Doc().Snapshot()
	if err != nil || len(snapshot) == 0 || snapshot[0] != 0 {
		return "", false
	}
	return ydoc.FingerprintOf(snapshot), true
}

func (f *File) raiseBase(s *collab.Session, version int64) {
	var generation string
	if !f.host.Do(func() { generation = f.generation }) {
		return
	}
	if !f.claimHolds(ClaimBase, version, s) {
		return
	}
	f.host.Do(func() {
		if generation != f.generation || f.restarting || f.host.Purged() || !f.loaded() || version <= f.baseVersion {
			return
		}
		f.baseVersion = version
		f.resolveTries.count = 0
		f.persistState()
		if f.resolving != nil && version >= f.resolving.version {
			f.dropResolve()
		}
		f.announceSoon()
	})
}

func (f *File) claimHolds(claim Claim, version int64, s *collab.Session) bool {
	var call *claimCall
	waiting, dropped, fromWriter := false, false, false
	var key string
	if !f.host.Do(func() {
		fromWriter = s == f.writer
		key = string(claim) + ":" + strconv.FormatInt(version, 10) + ":" + strconv.FormatBool(fromWriter) + ":" + s.UserID()
		if pending, ok := f.claims[key]; ok {
			call, waiting = pending, true
			return
		}
		if !fromWriter && len(f.claims) >= maxClaims {
			dropped = true
			f.host.Log("file_claim_dropped", map[string]any{"claim": claim, "version": version})
			return
		}
		call = &claimCall{done: make(chan struct{})}
		f.claims[key] = call
	}) || dropped {
		return false
	}
	if waiting {
		<-call.done
		return call.holds
	}
	holds := f.checkClaim(claim, version, s, fromWriter)
	f.host.Do(func() { delete(f.claims, key) })
	call.holds = holds
	close(call.done)
	return holds
}

func (f *File) checkClaim(claim Claim, version int64, s *collab.Session, fromWriter bool) bool {
	userID := s.UserID()
	drive, refused, err := readVersions(f.drive, f.host.SpaceID(), f.host.Options().File, userID)
	if err != nil {
		f.host.Do(func() {
			f.host.Log("file_claim_unverified", map[string]any{"claim": claim, "version": version, "writer": fromWriter})
		})
		return fromWriter
	}
	var saved int64
	f.host.Do(func() { saved = f.savedVersion() })
	if !refused && ClaimHolds(claim, version, drive, saved) && (claim != ClaimBase || BaseClaimantHolds(drive, userID, fromWriter)) {
		return true
	}
	f.host.Do(func() {
		f.host.Log("file_claim_refused", map[string]any{"claim": claim, "version": version, "writer": fromWriter})
	})
	return false
}

func (f *File) useContentVersions() {
	if f.contentMode || f.restarting {
		return
	}
	f.contentMode = true
	f.host.Log("file_content_mode", map[string]any{})
	f.persistState()
}

func (f *File) renamed(version int64) {
	if f.contentMode || version == 0 || !f.loaded() || version <= f.knownVersion() {
		return
	}
	f.renamedTo = version
	f.persistState()
	f.announceSoon()
}

func (f *File) Changed() {
	f.revision++
	was := f.dirty
	f.dirty = f.loaded()
	if f.dirty && !was {
		f.quietSince = time.Now()
	}
	f.announceSoon()
	f.checkWriterIfQuiet()
}

func (f *File) setWriter(s *collab.Session) {
	if f.writer == s {
		return
	}
	f.writer = s
	f.quietSince = time.Now()
	f.answeredAt = -1
	f.endCheck()
	f.checkWriterIfQuiet()
}

func (f *File) endCheck() {
	if f.check == nil {
		return
	}
	f.check.stop()
	f.check = nil
}

func (f *File) checkWriterIfQuiet() {
	if f.quietStop != nil {
		f.quietStop()
		f.quietStop = nil
	}
	if f.check != nil || f.resolving != nil || f.isText() || !f.dirty || f.writer == nil || f.host.Purged() ||
		f.revision == f.answeredAt || f.restarting {
		return
	}
	if left := time.Until(f.quietSince.Add(writerQuiet)); left > 0 {
		f.quietStop = f.host.After(left, f.checkWriterIfQuiet)
		return
	}
	if !f.writerIsHere() {
		return
	}
	nonce := newID()
	writer := f.writer
	f.check = &writerCheck{nonce: nonce, writer: writer}
	f.check.stop = f.host.After(writerAnswer, func() { f.writerSilent(writer, nonce) })
	f.send(writer, map[string]any{"type": "writer_check", "nonce": nonce})
}

func (f *File) writerAnswered(s *collab.Session, nonce string) {
	if len(nonce) > maxNonceLength || f.check == nil || f.check.writer != s || f.check.nonce != nonce {
		return
	}
	f.endCheck()
	f.answeredAt = f.revision
	f.quietSince = time.Now()
	f.checkWriterIfQuiet()
}

func (f *File) writerSilent(writer *collab.Session, nonce string) {
	if f.check == nil || f.check.nonce != nonce {
		return
	}
	f.check = nil
	if f.writer != writer {
		return
	}
	next := f.nextWriter(writer)
	f.host.Log("file_writer_silent", map[string]any{"moved": next != nil})
	if next == nil {
		f.answeredAt = f.revision
		f.quietSince = time.Now()
		return
	}
	f.setWriter(next)
	f.tellAll()
}

func (f *File) nextWriter(skip *collab.Session) *collab.Session {
	for _, other := range f.host.Sessions() {
		if other == skip || f.released[other] || other.ReadOnly() {
			continue
		}
		if other.UserID() != "" {
			return other
		}
	}
	return nil
}

func (f *File) handOver() {
	leaving := f.writer
	f.setWriter(nil)
	f.setWriter(f.nextWriter(leaving))
	f.tellAll()
}

func (f *File) recordSaved(fingerprint string) {
	f.savedFingerprint = fingerprint
	f.persistState()
	f.settleDirty()
}

func (f *File) settleDirty() {
	f.dirty = f.loaded() && f.fingerprint() != f.savedFingerprint
	f.announceSoon()
	f.checkWriterIfQuiet()
}

func (f *File) noteEditor(userID string) {
	if len(f.editors) > 0 && f.editors[0] == userID {
		return
	}
	editors := []string{userID}
	for _, editor := range f.editors {
		if editor != userID {
			editors = append(editors, editor)
		}
	}
	if len(editors) > maxEditors {
		editors = editors[:maxEditors]
	}
	f.editors = editors
	f.persistState()
}

func (f *File) mayReload(s *collab.Session) bool {
	return f.writer == s || (f.isText() && f.present(s))
}
