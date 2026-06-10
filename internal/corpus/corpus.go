// Package corpus reads Claude Code JSONL transcripts and extracts the
// assistant's user-facing prose turns.
package corpus

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one assistant message's user-facing text.
type Turn struct {
	Time    time.Time
	Project string // top-level dir under the transcript root, ~ one project
	Text    string
}

type entry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Read walks root for *.jsonl transcripts and returns assistant text turns
// with timestamps >= since. Sidechain (subagent) turns and subagents/
// directories are skipped: the target corpus is prose actually read in the
// main session.
//
// Files whose mtime predates since are skipped without parsing — a
// transcript's mtime is its last entry's write time, so an older file
// cannot contain in-window entries.
func Read(root string, since time.Time) ([]Turn, error) {
	// A missing root must be an error, not an empty result: for a drift
	// detector, a typo'd -dir that silently reads as "no signal this week"
	// is the worst failure shape. Unreadable subtrees below the root are
	// still skipped.
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var turns []Turn
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, don't abort the scan
		}
		if d.IsDir() {
			if d.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		ts, err := readFile(path, since)
		if err != nil {
			return nil // malformed file: skip
		}
		proj := projectOf(root, path)
		for i := range ts {
			ts[i].Project = proj
		}
		turns = append(turns, ts...)
		return nil
	})
	return turns, err
}

func projectOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	return parts[0]
}

func readFile(path string, since time.Time) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var turns []Turn
	// bufio.Reader, not Scanner: transcript lines (pasted images, big tool
	// results) routinely exceed any fixed scanner buffer.
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, tooLong, err := readLine(r)
		if !tooLong && len(line) > 0 {
			if t, ok := parseLine(line, since); ok {
				turns = append(turns, t)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return turns, err
		}
	}
	return turns, nil
}

// maxLineBytes caps a single transcript line. Real assistant entries top
// out in the low megabytes; a pathological no-newline blob must be skipped
// rather than accumulated into memory.
const maxLineBytes = 64 << 20

// readLine reads one newline-terminated line, reporting (and discarding
// the remainder of) lines over maxLineBytes.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > maxLineBytes {
				tooLong, line = true, nil
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, tooLong, err
	}
}

func parseLine(line []byte, since time.Time) (Turn, bool) {
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return Turn{}, false
	}
	if e.Type != "assistant" || e.IsSidechain {
		return Turn{}, false
	}
	ts, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil || ts.Before(since) {
		return Turn{}, false
	}
	var blocks []contentBlock
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return Turn{}, false
	}
	var parts []string
	for _, b := range blocks {
		// text only: thinking and tool_use are not user-facing prose
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	if len(parts) == 0 {
		return Turn{}, false
	}
	return Turn{Time: ts, Text: strings.Join(parts, "\n")}, true
}
