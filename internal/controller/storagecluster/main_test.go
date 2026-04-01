package storagecluster

import (
	"os"
	"testing"

	"github.com/red-hat-storage/ocs-operator/v4/pkg/platform"
)

func TestMain(m *testing.M) {
	platform.SetFakePlatformInstanceForTesting(true, "")
	exitCode := m.Run()
	platform.UnsetFakePlatformInstanceForTesting()
	os.Exit(exitCode)
}
