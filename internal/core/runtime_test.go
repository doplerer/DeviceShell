package core

import (
	"os"
	"testing"
	"time"
)

func newTestRuntime() *Service {
	return New()
}
func TestInventoryExpansionAndIdentity(t *testing.T) {
	newTestRuntime().testInventoryExpansionAndIdentity(t)
}
func TestInvalidInventoryPreserved(t *testing.T) {
	newTestRuntime().testInvalidInventoryPreserved(t)
}
func TestOutputLayout(t *testing.T) {
	newTestRuntime().testOutputLayout(t)
}
func TestHandshake(t *testing.T) {
	newTestRuntime().testHandshake(t)
}
func TestReleaseHeldInput(t *testing.T) {
	newTestRuntime().testReleaseHeldInput(t)
}
func TestProtocolFragmentation(t *testing.T) {
	newTestRuntime().testProtocolFragmentation(t)
}
func TestScale(t *testing.T) {
	newTestRuntime().testScale(t)
}
func TestQuoteAndRoutine(t *testing.T) {
	newTestRuntime().testQuoteAndRoutine(t)
}
func TestCancel(t *testing.T) {
	newTestRuntime().testCancel(t)
}
func TestNormalizeURL(t *testing.T) {
	newTestRuntime().testNormalizeURL(t)
}
func TestGridLayout(t *testing.T) {
	newTestRuntime().testGridLayout(t)
}
func TestPackStep(t *testing.T) {
	newTestRuntime().testPackStep(t)
}
func TestParseDisplay(t *testing.T) {
	newTestRuntime().testParseDisplay(t)
}
func TestMatchDisplay(t *testing.T) {
	newTestRuntime().testMatchDisplay(t)
}
func TestResolutionSpec(t *testing.T) {
	newTestRuntime().testResolutionSpec(t)
}
func TestViewBitRate(t *testing.T) {
	newTestRuntime().testViewBitRate(t)
}
func TestWindowBackoff(t *testing.T) {
	newTestRuntime().testWindowBackoff(t)
}
func TestSuperviseWindow(t *testing.T) {
	newTestRuntime().testSuperviseWindow(t)
}

// TestHelperSleep is a child process for TestSuperviseWindow: a launch that never shows a window.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("DEVICESHELL_TEST_SLEEP") != "1" {
		t.Skip()
	}
	time.Sleep(30 * time.Second)
}
func TestScreenArgs(t *testing.T) {
	newTestRuntime().testScreenArgs(t)
}
func TestStorageReset(t *testing.T) {
	newTestRuntime().testStorageReset(t)
}
func TestStorageMigrationPrivacy(t *testing.T) {
	newTestRuntime().testStorageMigrationPrivacy(t)
}
func TestStorageLimits(t *testing.T) {
	newTestRuntime().testStorageLimits(t)
}
func TestStorageLockChild(t *testing.T) {
	newTestRuntime().testStorageLockChild(t)
}
func TestStorageLockAcrossProcesses(t *testing.T) {
	newTestRuntime().testStorageLockAcrossProcesses(t)
}
func TestStorageDiagnosticBound(t *testing.T) {
	newTestRuntime().testStorageDiagnosticBound(t)
}
func TestStoragePartialFailure(t *testing.T) {
	newTestRuntime().testStoragePartialFailure(t)
}
func TestStorageResetRedirect(t *testing.T) {
	newTestRuntime().testStorageResetRedirect(t)
}
func TestNewActionsValidation(t *testing.T) {
	newTestRuntime().testNewActionsValidation(t)
}
func TestStepRetryAndContinue(t *testing.T) {
	newTestRuntime().testStepRetryAndContinue(t)
}
func TestStepTimeoutAndCancel(t *testing.T) {
	newTestRuntime().testStepTimeoutAndCancel(t)
}
func TestBuiltinVars(t *testing.T) {
	newTestRuntime().testBuiltinVars(t)
}
func TestParseHealth(t *testing.T) {
	newTestRuntime().testParseHealth(t)
}
func TestHealthAlerts(t *testing.T) {
	newTestRuntime().testHealthAlerts(t)
}
