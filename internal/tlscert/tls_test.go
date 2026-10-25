package tlscert

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestKeyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	d := t.TempDir()
	c, k := filepath.Join(d, "cert.pem"), filepath.Join(d, "key.pem")
	if err := EnsureCert(c, k); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(k); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", st.Mode().Perm())
	}
	os.Chmod(k, 0o644)
	if err := EnsureCert(c, k); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(k); st.Mode().Perm() != 0o600 {
		t.Errorf("existing key mode %o", st.Mode().Perm())
	}
}
