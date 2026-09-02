package agent

import (
	"testing"
	"time"
)

func TestParseIPVSStats(t *testing.T) {
	input := `IP Virtual Server version 1.2.1 (size=4096)
Prot LocalAddress:Port               Conns   InPkts  OutPkts  InBytes OutBytes
UDP  0.0.0.0:8005                       12       15       14     9000     8000
  -> 5.199.169.230:8004                  12       15       14     9000     8000
`
	stats, err := ParseIPVSStats(input, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].Connections != 12 || stats[1].BackendAddress != "5.199.169.230" || stats[1].IncomingBytes != 9000 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}
