package agent

import "testing"

func TestRoundLimitDefaults(t *testing.T) {
	e := &Engine{}
	if got := e.roundLimit(); got != DefaultToolRounds {
		t.Fatalf("默认轮数上限 = %d，want %d", got, DefaultToolRounds)
	}
	e.MaxToolRounds = 7
	if got := e.roundLimit(); got != 7 {
		t.Fatalf("用户上限 = %d，want 7", got)
	}
	e.maxRound = 3
	if got := e.roundLimit(); got != 3 {
		t.Fatalf("子代理上限 = %d，want 3", got)
	}
}
