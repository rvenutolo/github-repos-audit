package rules_test

import (
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

var ciEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// ciCommit is a finished commit that waited the given seconds.
func ciCommit(wait int64, jobs ...audit.CIJob) audit.CICommit {
	return audit.CICommit{OID: "0000000000000000000000000000000000000000", Complete: true, WaitSeconds: wait, Jobs: jobs}
}

// ciPRs makes finished human pull requests merged one day apart, in order.
func ciPRs(waits ...int64) []audit.CICommit {
	out := make([]audit.CICommit, 0, len(waits))
	for i, w := range waits {
		c := ciCommit(w)
		c.PRNumber = i + 1
		c.MergedAt = ciEpoch.Add(time.Duration(i) * 24 * time.Hour)
		out = append(out, c)
	}
	return out
}

func ciMain(waits ...int64) []audit.CICommit {
	out := make([]audit.CICommit, 0, len(waits))
	for _, w := range waits {
		out = append(out, ciCommit(w))
	}
	return out
}

func ciRepo(name string, prs, main []audit.CICommit) audit.Repo {
	r := fixture(name, "tools")
	r.CI = audit.CI{PullRequests: prs, DefaultBranch: main}
	return r
}

func ciLines(t *testing.T, repos ...audit.Repo) []rules.CITimingLine {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(), Repos: repos})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.CITiming
}

func TestCITiming_meanOfLastFive(t *testing.T) {
	t.Parallel()

	got := ciLines(t, ciRepo("a", ciPRs(100, 200, 300, 400, 500, 600, 700), nil))
	if len(got) != 1 || !got[0].HasPR || got[0].PR != 500*time.Second {
		t.Fatalf("CITiming = %+v, want one line with PR mean 500s", got)
	}
}

func TestCITiming_botOnlyPRsShowNA(t *testing.T) {
	t.Parallel()

	prs := ciPRs(100, 200)
	for i := range prs {
		prs[i].AuthorBot = true
	}
	got := ciLines(t, ciRepo("a", prs, ciMain(60)))
	if len(got) != 1 || got[0].HasPR || !got[0].HasMain || got[0].Main != 60*time.Second {
		t.Fatalf("CITiming = %+v, want HasPR=false HasMain=true Main=60s", got)
	}
}

func TestCITiming_skipsIncompleteCommits(t *testing.T) {
	t.Parallel()

	main := ciMain(10, 1000, 20, 30, 40, 50)
	main[1].Complete = false
	got := ciLines(t, ciRepo("a", nil, main))
	if len(got) != 1 || got[0].Main != 30*time.Second {
		t.Fatalf("CITiming = %+v, want Main mean 30s over the five complete commits", got)
	}
}

func TestCITiming_fewerThanFive(t *testing.T) {
	t.Parallel()

	got := ciLines(t, ciRepo("a", nil, ciMain(10, 31)))
	if len(got) != 1 || got[0].Main != 21*time.Second {
		t.Fatalf("CITiming = %+v, want Main mean 21s (20.5 rounded to the second)", got)
	}
}

func TestCITiming_slowestJobAcrossSamples(t *testing.T) {
	t.Parallel()

	prs := ciPRs(10)
	prs[0].Jobs = []audit.CIJob{{Label: "a / x", Seconds: 60}, {Label: "a / y", Seconds: 50}}
	main := ciMain(10)
	main[0].Jobs = []audit.CIJob{{Label: "a / y", Seconds: 200}}
	got := ciLines(t, ciRepo("r", prs, main))
	if len(got) != 1 || got[0].SlowestJob != "a / y" || got[0].SlowestMean != 125*time.Second {
		t.Fatalf("CITiming = %+v, want slowest a / y at 125s", got)
	}
}

func TestCITiming_slowestJobIgnoresSkippedRuns(t *testing.T) {
	t.Parallel()

	prs := ciPRs(10, 10)
	prs[0].Jobs = []audit.CIJob{{Label: "a / x", Seconds: 0}, {Label: "a / y", Seconds: 80}}
	prs[1].Jobs = []audit.CIJob{{Label: "a / x", Seconds: 100}}
	got := ciLines(t, ciRepo("r", prs, nil))
	if len(got) != 1 || got[0].SlowestJob != "a / x" || got[0].SlowestMean != 100*time.Second {
		t.Fatalf("CITiming = %+v, want slowest a / x at 100s", got)
	}
}

func TestCITiming_slowestJobTieBreaksByLabel(t *testing.T) {
	t.Parallel()

	main := ciMain(10)
	main[0].Jobs = []audit.CIJob{{Label: "b / z", Seconds: 70}, {Label: "a / z", Seconds: 70}}
	got := ciLines(t, ciRepo("r", nil, main))
	if len(got) != 1 || got[0].SlowestJob != "a / z" {
		t.Fatalf("CITiming = %+v, want the tie won by a / z", got)
	}
}

func TestCITiming_slowestJobIgnoresUnsampledCommits(t *testing.T) {
	t.Parallel()

	main := ciMain(10, 10, 10, 10, 10, 10)
	main[5].Jobs = []audit.CIJob{{Label: "old / job", Seconds: 999}}
	got := ciLines(t, ciRepo("r", nil, main))
	if len(got) != 1 || got[0].SlowestJob != "" {
		t.Fatalf("CITiming = %+v, want no slowest job: the sixth commit is not sampled", got)
	}
}

func TestCITiming_omitsRepoWithoutCI(t *testing.T) {
	t.Parallel()

	if got := ciLines(t, ciRepo("a", nil, nil)); len(got) != 0 {
		t.Fatalf("CITiming = %+v, want no line", got)
	}
}

func TestCITiming_order(t *testing.T) {
	t.Parallel()

	got := ciLines(t,
		ciRepo("slow", ciPRs(300), nil),
		ciRepo("fast", ciPRs(60), nil),
		ciRepo("nopr", nil, ciMain(900)),
		ciRepo("nopr-b", nil, ciMain(950)),
		ciRepo("Tie", ciPRs(60), nil),
	)
	names := make([]string, 0, len(got))
	for _, l := range got {
		names = append(names, l.Repo)
	}
	want := []string{"slow", "fast", "Tie", "nopr-b", "nopr"}
	if len(names) != len(want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}
