package gitrepo

import (
	"fmt"
	"strconv"
	"strings"
)

type File struct {
	Status string
	Path   string
}

type Repository struct {
	Path, CommonDir, Branch, Upstream      string
	Staged, Unstaged, Untracked, Conflicts int
	Ahead, Behind                          int
	Detached, Unborn                       bool
	TrackingKnown                          bool
	Files                                  []File
	Error                                  string
}

func (r Repository) Changed() bool {
	return r.Staged+r.Unstaged+r.Untracked+r.Conflicts > 0
}

// ParseStatus consumes porcelain v2 with NUL delimiters, including rename pairs.
func ParseStatus(data []byte) (Repository, error) {
	var r Repository
	records := strings.Split(string(data), "\x00")
	for i := 0; i < len(records); i++ {
		s := records[i]
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "# ") {
			switch {
			case strings.HasPrefix(s, "# branch.head "):
				r.Branch = strings.TrimPrefix(s, "# branch.head ")
				r.Detached = r.Branch == "(detached)"
			case strings.HasPrefix(s, "# branch.oid "):
				r.Unborn = strings.TrimPrefix(s, "# branch.oid ") == "(initial)"
			case strings.HasPrefix(s, "# branch.upstream "):
				r.Upstream = strings.TrimPrefix(s, "# branch.upstream ")
			case strings.HasPrefix(s, "# branch.ab "):
				fields := strings.Fields(s)
				if len(fields) != 4 {
					return r, fmt.Errorf("invalid branch counts: %q", s)
				}
				a, e1 := strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				b, e2 := strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
				if e1 != nil || e2 != nil || a < 0 || b < 0 {
					return r, fmt.Errorf("invalid branch counts: %q", s)
				}
				r.Ahead, r.Behind = a, b
				r.TrackingKnown = true
			}
			continue
		}
		if strings.HasPrefix(s, "? ") {
			r.Untracked++
			r.Files = append(r.Files, File{"??", s[2:]})
			continue
		}
		if strings.HasPrefix(s, "! ") {
			continue
		}
		n := 9
		switch s[0] {
		case '1':
		case '2':
			n = 10
		case 'u':
			n = 11
		default:
			return r, fmt.Errorf("unknown status record: %q", s)
		}
		f := strings.SplitN(s, " ", n)
		if len(f) != n || len(f[1]) != 2 {
			return r, fmt.Errorf("invalid status record: %q", s)
		}
		if s[0] == 'u' {
			r.Conflicts++
		} else {
			if f[1][0] != '.' {
				r.Staged++
			}
			if f[1][1] != '.' {
				r.Unstaged++
			}
		}
		r.Files = append(r.Files, File{f[1], f[n-1]})
		if s[0] == '2' {
			i++ // Original filename is its own NUL-delimited record.
			if i >= len(records) || records[i] == "" {
				return r, fmt.Errorf("missing rename source")
			}
		}
	}
	return r, nil
}
