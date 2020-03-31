package tests

import (
	"os/exec"

	"sigs.k8s.io/kube-storage-version-migrator/test/e2e/util"
)

func setupMigrator() {
	// setup the CRD to test.
	// We install the CRD before the migrator so that the migrator sees the
	// CRD in the first round of discovery.
	testCRD := "../../test/e2e/crd.yaml"
	// setup the migration system
	crds := []string{"../../manifests.local/storage_migration_crd.yaml", "../../manifests.local/storage_state_crd.yaml"}
	rbacs := "../../manifests.local/namespace-rbac.yaml"
	trigger := "../../manifests.local/trigger.yaml"
	migrator := "../../manifests.local/migrator.yaml"
	output, err := exec.Command("kubectl", "apply", "-f", testCRD).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", crds[0], "-f", crds[1]).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", rbacs).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", migrator).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
	output, err = exec.Command("kubectl", "apply", "-f", trigger).CombinedOutput()
	if err != nil {
		util.Failf("%s", output)
	}
}
