package cfg

import (
	"os"
	"testing"
)

func TestLoadFile(t *testing.T) {
	// Create a temporary config file for testing
	f, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write some fake YAML to the file
	if _, err := f.WriteString(`
token: mytoken
shard: myshard
shardcount: 4
owner: me
port: 8080
redirecturl: http://localhost:8080/callback
ci: 42
cs: mysecretstring
`); err != nil {
		t.Fatal(err)
	}

	f.Close()

	// Load the config file and assert that the values are correct
	config := loadFile(f.Name())
	if config.Token != "mytoken" {
		t.Errorf("unexpected token: %s", config.Token)
	}
	if config.Shard != "myshard" {
		t.Errorf("unexpected shard: %s", config.Shard)
	}
	if config.ShardCount != "4" {
		t.Errorf("unexpected shard count: %s", config.ShardCount)
	}
	if config.Owner != "me" {
		t.Errorf("unexpected owner: %s", config.Owner)
	}
	if config.Port != 8080 {
		t.Errorf("unexpected port: %d", config.Port)
	}
	if config.RedirectURL != "http://localhost:8080/callback" {
		t.Errorf("unexpected redirect URL: %s", config.RedirectURL)
	}
	if config.Ci != 42 {
		t.Errorf("unexpected ci: %d", config.Ci)
	}
	if config.Cs != "mysecretstring" {
		t.Errorf("unexpected cs: %s", config.Cs)
	}
}
