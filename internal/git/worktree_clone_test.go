package git

import "testing"

func TestReferencesPath(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		text, rel string
		want      bool
	}{
		{"scripts/android-docker.sh test", "scripts/android-docker.sh", true},
		{"读 docker/Dockerfile。", "docker", true},
		{"`docker/`", "docker", true},
		{"./scripts/a.sh", "scripts/a.sh", true},
		{"mydocker/x", "docker", false},
		{"docker-compose.yml", "docker", false},
		{"scripts/android-docker.shx", "scripts/android-docker.sh", false},
		{"docker. next", "docker", true},
		{"docker.x docker/", "docker", true},
	} {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()
			if got := referencesPath(tt.text, tt.rel); got != tt.want {
				t.Errorf("referencesPath(%q, %q) = %v, want %v", tt.text, tt.rel, got, tt.want)
			}
		})
	}
}
