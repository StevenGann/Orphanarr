package scan

import "testing"

// DA-6: the overlap collapse in pipeline.ScanNow is not transitive, so a
// three-way cross-seed produces two plans for one payload.
//
// ScanNow:
//
//	for i, c := range candidates {
//	    if claimed[i] { continue }          // <- peers of a claimed candidate
//	    for _, peer := range ov.Peers(i, c) //    are never claimed themselves
//	        claimed[peer] = true
//	    ...plan c...
//	}
//
// A(0) and B(1) share a fingerprint. B(1) and C(2) share a path. A and C
// share neither. i=0 claims 1 and plans A. i=1 is claimed and `continue`s
// BEFORE claiming 2. i=2 is unclaimed and gets its own plan — a second
// destination for the same bytes, which is exactly what I4 forbids.
func TestDA6_OverlapCollapseIsNotTransitive(t *testing.T) {
	a := Candidate{LocalPaths: []string{"/data/t/a/movie.mkv"}, Fingerprint: "F"}
	b := Candidate{LocalPaths: []string{"/data/t/shared/movie.mkv"}, Fingerprint: "F"}
	c := Candidate{LocalPaths: []string{"/data/t/shared/movie.mkv"}, Fingerprint: "G"}

	cands := []Candidate{a, b, c}
	ov := NewOverlap()
	for i, cd := range cands {
		ov.Add(i, cd, nil)
	}

	// ScanNow's loop, using the transitive closure rather than the direct
	// peer set. Peers alone claimed B and moved on before ever asking what
	// B overlapped, so C was planned separately for the same bytes.
	claimed := map[int]bool{}
	var planned []int
	for i := range cands {
		if claimed[i] {
			continue
		}
		for _, peer := range ov.Transitive(i) {
			claimed[peer] = true
		}
		planned = append(planned, i)
	}

	if len(planned) != 1 {
		t.Errorf("%d candidates were planned (%v) for one payload. "+
			"A~B by fingerprint, B~C by path, so all three are the same content; "+
			"I4 requires them to collapse into one unit of work.", len(planned), planned)
	}
}

// The categorised half of O10: an uncategorised candidate that shares a
// payload with a torrent an *arr already owns must be BLOCKED, not merely
// collapsed. This is DESIGN §3.4 FP-1, ranked highest likelihood x damage.
func TestCategorisedPeerBlocksTheCandidate(t *testing.T) {
	orphan := Candidate{LocalPaths: []string{"/data/t/movie.mkv"}, Fingerprint: "F"}
	sonarr := Candidate{LocalPaths: []string{"/data/t/movie.mkv"}, Fingerprint: "F"}

	ov := NewOverlap()
	ov.Add(0, orphan, nil)

	blocked := ov.AddPeers([]Candidate{sonarr}, nil)
	if !blocked[0] {
		t.Fatal("an uncategorised torrent sharing a path with a categorised one " +
			"was not blocked; filing it duplicates content an *arr already imported")
	}
}

// And a candidate that overlaps nothing categorised must survive.
func TestUnrelatedCandidateIsNotBlocked(t *testing.T) {
	orphan := Candidate{LocalPaths: []string{"/data/t/a.mkv"}, Fingerprint: "A"}
	other := Candidate{LocalPaths: []string{"/data/t/b.mkv"}, Fingerprint: "B"}

	ov := NewOverlap()
	ov.Add(0, orphan, nil)
	if ov.AddPeers([]Candidate{other}, nil)[0] {
		t.Fatal("a candidate sharing nothing with a categorised torrent was blocked")
	}
}
