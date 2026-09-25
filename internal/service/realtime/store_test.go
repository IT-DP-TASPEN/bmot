package realtime

import (
	"context"
	"testing"
)

func TestAppStoreRejectsSourceDatabases(t *testing.T) {
	for _, name := range []string{"dwhv2", "newsinergi"} {
		if store, err := Open(context.Background(), "user:password@tcp(127.0.0.1:1)/"+name); err == nil {
			store.Close()
			t.Fatalf("accepted source database %s", name)
		}
	}
}
