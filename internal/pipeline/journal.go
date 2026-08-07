package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/StevenGann/Orphanarr/internal/exec"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// ndjson appends every completed operation to a flat text file.
//
// This is the second half of I7, and it is deliberately redundant with the
// database. SQLite corruption, a botched migration, a `docker run` with the
// wrong -v, or a user deleting /config all destroy the DB — and at that
// moment there is exactly one question worth answering: WHERE DID MY FILES
// GO. A flat file the user can grep from a rescue shell answers it. It
// costs one append per operation.
//
// The package comment on exec.Journal asserted this existed for two
// releases before it did. A comment describing behaviour the code does not
// have is worse than no comment, because it stops the next person looking.
type ndjson struct {
	mu  sync.Mutex
	dir string
}

func newNDJSON(configDir string) *ndjson {
	return &ndjson{dir: filepath.Join(configDir, "journal")}
}

// entry is one line. Field names are short and stable: this is meant to be
// read by a human with grep, under pressure, without documentation.
type entry struct {
	TS     string `json:"ts"`
	Op     int64  `json:"op"`
	Plan   int64  `json:"plan"`
	Method string `json:"method"`
	Src    string `json:"src"`
	Dest   string `json:"dest"`
	Bytes  int64  `json:"bytes"`
}

func (j *ndjson) append(planID int64, s *exec.Step) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	// One file per month, so a long-running instance stays greppable
	// without a rotation mechanism nobody would maintain.
	now := time.Now().UTC()
	path := filepath.Join(j.dir, now.Format("2006-01")+".ndjson")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(entry{
		TS: now.Format(time.RFC3339), Op: int64(s.Seq), Plan: planID,
		Method: string(s.Actual), Src: s.Src, Dest: s.Dst, Bytes: s.Bytes,
	})
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	// fsync, because a journal that loses its last entries in the crash it
	// exists to survive is decoration.
	return f.Sync()
}

// journal writes every mutation to the database before it is attempted and
// again after it succeeds (I7), and mirrors successes to the flat file.
type journal struct {
	db     *store.DB
	nd     *ndjson
	planID int64
}

func newJournal(db *store.DB, nd *ndjson, planID int64) *journal {
	return &journal{db: db, nd: nd, planID: planID}
}

func (j *journal) Before(ctx context.Context, s *exec.Step) error {
	// Status and destination only. Writing a zero PlanStep here cleared
	// created_by_us and created_dirs_json on every step of a RETRIED plan,
	// so the files placed by the first attempt became unremovable: rollback
	// keys on created_by_us and would never see them again.
	//
	// Uses a cancellation-free context: this is the record of what we are
	// about to do, and it must survive the request that triggered it being
	// abandoned.
	return j.db.MarkStepInProgress(context.WithoutCancel(ctx), j.planID, s.Seq, s.Dst)
}

func (j *journal) After(ctx context.Context, s *exec.Step) error {
	ctx = context.WithoutCancel(ctx)
	if err := j.db.UpdateStepResult(ctx, store.PlanStep{
		PlanID: j.planID, Seq: s.Seq, DstPath: s.Dst,
		MethodActual: string(s.Actual), CreatedByUs: s.CreatedByUs,
		CreatedDirs: s.CreatedDirs, Status: "done",
	}); err != nil {
		return err
	}
	// A flat-journal failure must NOT fail the placement: the file is
	// already on disk, and refusing to acknowledge it would be worse than
	// an incomplete audit trail. Surface it and continue.
	if j.nd != nil {
		if err := j.nd.append(j.planID, s); err != nil {
			j.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "JOURNAL_WRITE_FAILED",
				Message: fmt.Sprintf("could not append to the flat journal: %v", err),
			})
		}
	}
	return nil
}
