package destrules

import (
	"strings"
	"testing"
)

// TestClassifyStateful pins the outside-the-repo destruction family. The guard
// asks the operator for exactly these forms; disabling any one of them is a
// silent bypass, so every documented rule gets a direct case (the 2026-10-28
// audit found this package had no tests and a total disable survived the
// execclassify/guard suites).
func TestClassifyStateful(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantHit bool
		wantWhy string // substring; empty = no requirement
	}{
		// git: every push publishes; only --dry-run stays benign.
		{"git push", "git push origin main", true, "publishes to a remote"},
		{"git push force flag", "git push --force origin main", true, "rewrites remote history"},
		{"git push short force", "git push -f", true, "rewrites remote history"},
		{"git push +refspec", "git push origin +main", true, "force-updates the remote ref"},
		{"git push dry run", "git push --dry-run origin main", false, ""},
		{"git push short dry run", "git push -n origin main", false, ""},
		{"git push bare", "git push", true, "publishes to a remote"},

		// git checkout/restore: a whole-tree discard only.
		{"git checkout dot", "git checkout .", true, "discards all uncommitted changes"},
		{"git checkout dashdot", "git checkout -- .", true, "discards all uncommitted changes"},
		{"git checkout one file", "git checkout -- file.go", false, ""},
		{"git restore dot", "git restore .", true, "discards all uncommitted changes"},
		{"git restore staged", "git restore --staged .", false, ""},

		// kubectl: deletion of state the repo cannot restore.
		{"kubectl delete namespace", "kubectl delete namespace prod", true, "kubectl delete"},
		{"kubectl delete pvc", "kubectl delete pvc data-0", true, "kubectl delete"},
		{"kubectl delete secret", "kubectl delete secret db-creds", true, "kubectl delete"},
		{"kubectl delete all", "kubectl delete pods --all", true, "kubectl delete"},
		{"kubectl delete file", "kubectl delete -f manifest.yaml", true, "kubectl delete"},
		{"kubectl delete routine pod", "kubectl delete pod api-0", false, ""},
		{"kubectl get", "kubectl get pods", false, ""},

		// terraform/tofu destroy.
		{"terraform destroy", "terraform destroy -auto-approve", true, "tears down managed infrastructure"},
		{"tofu destroy", "tofu destroy", true, "tears down managed infrastructure"},
		{"terraform plan", "terraform plan", false, ""},

		// aws s3: recursive/force removal of remote objects.
		{"aws s3 rm recursive", "aws s3 rm s3://bucket/prefix --recursive", true, "deletes remote objects"},
		{"aws s3 rb force", "aws s3 rb s3://bucket --force", true, "deletes remote objects"},
		{"aws s3 single object", "aws s3 rm s3://bucket/key.txt", false, ""},
		{"aws s3 ls", "aws s3 ls s3://bucket", false, ""},

		// databases: destructive DDL/DML.
		{"psql drop table", `psql -c 'DROP TABLE users'`, true, "drop table"},
		{"psql drop database", `psql -c 'DROP DATABASE prod'`, true, "drop database"},
		{"mysql truncate", `mysql -e 'TRUNCATE TABLE orders'`, true, "truncate table"},
		{"sqlite delete from", `sqlite3 app.db 'DELETE FROM users'`, true, "delete from"},
		{"psql select", `psql -c 'select 1'`, false, ""},

		// containers: volume/image pruning.
		{"docker system prune", "docker system prune -af", true, "prune deletes"},
		{"docker volume rm", "docker volume rm pgdata", true, "volume rm deletes persisted data"},
		{"docker compose down -v", "docker compose down -v", true, "deletes named volumes"},
		{"docker ps", "docker ps", false, ""},

		// unrelated commands stay governed, not gated.
		{"plain ls", "ls -la", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := strings.Fields(tc.cmd)
			hit, why := ClassifyStateful(tokens[0], tc.cmd, tokens)
			if hit != tc.wantHit {
				t.Fatalf("ClassifyStateful(%q) hit = %v (%q), want %v", tc.cmd, hit, why, tc.wantHit)
			}
			if tc.wantHit && tc.wantWhy != "" && !strings.Contains(why, tc.wantWhy) {
				t.Fatalf("ClassifyStateful(%q) why = %q, want substring %q", tc.cmd, why, tc.wantWhy)
			}
			if !tc.wantHit && why != "" {
				t.Fatalf("ClassifyStateful(%q) returned a reason for a non-hit: %q", tc.cmd, why)
			}
		})
	}
}
