// Package diag is the shape every check in this project reports in.
//
// One shape, used by the core and by adapters alike, so that a finding reads the
// same wherever it came from -- and so that the severity ordering that decides
// the exit code lives in exactly one place.
package diag

import "fmt"

// Severity levels, worst first.
const (
	Critical = "CRITICAL"
	High     = "HIGH"
	Medium   = "MEDIUM"
	Low      = "LOW"
	Info     = "INFO"
)

// Finding is one observation about the state of the machine. It follows the same
// normalized shape the verification contract will use, so that everything this
// project reports can be read the same way.
type Finding struct {
	Severity string
	Message  string
	Location string
}

// Report collects findings and the lines already shown to the reader.
type Report struct {
	Findings []Finding
}

// Add records a finding. Info-level notes are printed, not collected: they are
// context, not something anyone has to act on.
func (r *Report) Add(sev, msg, loc string) {
	r.Findings = append(r.Findings, Finding{sev, msg, loc})
}

var rank = map[string]int{Critical: 4, High: 3, Medium: 2, Low: 1, Info: 0}

// Worst is the highest severity present.
func (r *Report) Worst() string {
	w := Info
	for _, f := range r.Findings {
		if rank[f.Severity] > rank[w] {
			w = f.Severity
		}
	}
	return w
}

// Failed reports whether anything found is serious enough to fail a gate.
func (r *Report) Failed() bool {
	switch r.Worst() {
	case Critical, High:
		return true
	}
	return false
}

var marks = map[string]string{
	Critical: "!!", High: " !", Medium: " ~", Low: " -", Info: " .",
}

// Line prints one finding.
func Line(sev, msg, loc string) {
	fmt.Printf("%s %s\n", marks[sev], msg)
	if loc != "" {
		fmt.Printf("     %s\n", loc)
	}
}

// Print writes a finding as a line.
func (f Finding) Print() { Line(f.Severity, f.Message, f.Location) }
