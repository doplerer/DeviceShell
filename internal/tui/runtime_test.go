package tui

import (
	"testing"

	"github.com/doplerer/deviceshell/internal/core"
)

func newTestRuntime() *Interface {
	return New(core.New())
}
func TestLanguagePairsAndSaving(t *testing.T) {
	newTestRuntime().testLanguagePairsAndSaving(t)
}
func TestOldSpanishNamesStillWork(t *testing.T) {
	newTestRuntime().testOldSpanishNamesStillWork(t)
}
func TestEnglishHasNoSpanishLeftovers(t *testing.T) {
	newTestRuntime().testEnglishHasNoSpanishLeftovers(t)
}
func TestHealthAlertsShown(t *testing.T) {
	newTestRuntime().testHealthAlertsShown(t)
}
func TestAlertSettings(t *testing.T) {
	newTestRuntime().testAlertSettings(t)
}
func TestRetryMarks(t *testing.T) {
	newTestRuntime().testRetryMarks(t)
}
func TestSystemCommands(t *testing.T) {
	newTestRuntime().testSystemCommands(t)
}
func TestViewAndSyncCommands(t *testing.T) {
	newTestRuntime().testViewAndSyncCommands(t)
}
func TestViewReadOnlyFlag(t *testing.T) {
	newTestRuntime().testViewReadOnlyFlag(t)
}
func TestStorageResetConfirmation(t *testing.T) {
	newTestRuntime().testStorageResetConfirmation(t)
}
func TestStorageConfirmedReset(t *testing.T) {
	newTestRuntime().testStorageConfirmedReset(t)
}
func TestStorageSessionDetails(t *testing.T) {
	newTestRuntime().testStorageSessionDetails(t)
}
func TestFormTapStepByStep(t *testing.T) {
	newTestRuntime().testFormTapStepByStep(t)
}
func TestFormKeepsTypedArgsAndRoutineVars(t *testing.T) {
	newTestRuntime().testFormKeepsTypedArgsAndRoutineVars(t)
}
func TestFormGroupAndPicker(t *testing.T) {
	newTestRuntime().testFormGroupAndPicker(t)
}
func TestFormArrowsStepNumbers(t *testing.T) {
	newTestRuntime().testFormArrowsStepNumbers(t)
}
func TestCommandHintAndDestinationSuggestions(t *testing.T) {
	newTestRuntime().testCommandHintAndDestinationSuggestions(t)
}
func TestMotionMenuAndRetiredCommands(t *testing.T) {
	newTestRuntime().testMotionMenuAndRetiredCommands(t)
}
func TestMotionChooserAndBack(t *testing.T) {
	newTestRuntime().testMotionChooserAndBack(t)
}
func TestMotionRawTextAndHelp(t *testing.T) {
	newTestRuntime().testMotionRawTextAndHelp(t)
}
func TestPaletteListsActions(t *testing.T) {
	newTestRuntime().testPaletteListsActions(t)
}
func TestPaletteContextWithoutSections(t *testing.T) {
	newTestRuntime().testPaletteContextWithoutSections(t)
}
func TestPaletteEnterOpensAssistant(t *testing.T) {
	newTestRuntime().testPaletteEnterOpensAssistant(t)
}
func TestSearchByMeaning(t *testing.T) {
	newTestRuntime().testSearchByMeaning(t)
}
func TestDeeperArgumentSuggestions(t *testing.T) {
	newTestRuntime().testDeeperArgumentSuggestions(t)
}
func TestRecommendationContextPriority(t *testing.T) {
	newTestRuntime().testRecommendationContextPriority(t)
}
func TestRecommendationSubmitAfterTab(t *testing.T) {
	newTestRuntime().testRecommendationSubmitAfterTab(t)
}
func TestRecommendationTabOnly(t *testing.T) {
	newTestRuntime().testRecommendationTabOnly(t)
}
func TestRecommendationRightArrow(t *testing.T) {
	newTestRuntime().testRecommendationRightArrow(t)
}
func TestRecommendationDismissAndRefresh(t *testing.T) {
	newTestRuntime().testRecommendationDismissAndRefresh(t)
}
func TestRecommendationVisibilityAndLayout(t *testing.T) {
	newTestRuntime().testRecommendationVisibilityAndLayout(t)
}
func TestSettingsPersistenceAndValidation(t *testing.T) {
	newTestRuntime().testSettingsPersistenceAndValidation(t)
}
func TestSettingsGeometryAndRetiredCommands(t *testing.T) {
	newTestRuntime().testSettingsGeometryAndRetiredCommands(t)
}
func TestSettingsSelectorsAndWindowArea(t *testing.T) {
	newTestRuntime().testSettingsSelectorsAndWindowArea(t)
}
func TestSettingsArrowsStepNumbers(t *testing.T) {
	newTestRuntime().testSettingsArrowsStepNumbers(t)
}
func TestTokenize(t *testing.T) {
	newTestRuntime().testTokenize(t)
}
func TestParseTargetsAndRaw(t *testing.T) {
	newTestRuntime().testParseTargetsAndRaw(t)
}
func TestSuggestions(t *testing.T) {
	newTestRuntime().testSuggestions(t)
}
func TestRunBatch(t *testing.T) {
	newTestRuntime().testRunBatch(t)
}
func TestTarget(t *testing.T) {
	newTestRuntime().testTarget(t)
}
func TestRemovedSimulationRejected(t *testing.T) {
	newTestRuntime().testRemovedSimulationRejected(t)
}
func TestCancelAndExit(t *testing.T) {
	newTestRuntime().testCancelAndExit(t)
}
func TestBottomBarPinned(t *testing.T) {
	newTestRuntime().testBottomBarPinned(t)
}
func TestTranscriptScroll(t *testing.T) {
	newTestRuntime().testTranscriptScroll(t)
}
func TestViewsRender(t *testing.T) {
	newTestRuntime().testViewsRender(t)
}
func TestWelcomeLayout(t *testing.T) {
	newTestRuntime().testWelcomeLayout(t)
}
func TestLogoShine(t *testing.T) {
	newTestRuntime().testLogoShine(t)
}
func TestWelcomeTransitions(t *testing.T) {
	newTestRuntime().testWelcomeTransitions(t)
}
func TestHelpAndFullCommandMenu(t *testing.T) {
	newTestRuntime().testHelpAndFullCommandMenu(t)
}
func TestWheelMovesSuggestions(t *testing.T) {
	newTestRuntime().testWheelMovesSuggestions(t)
}
func TestTerminalPasteStaysOnOneLine(t *testing.T) {
	newTestRuntime().testTerminalPasteStaysOnOneLine(t)
}
func TestProgressBar(t *testing.T) {
	newTestRuntime().testProgressBar(t)
}
func TestWrapIndented(t *testing.T) {
	newTestRuntime().testWrapIndented(t)
}
func TestLineEditorSelectionAndClipboard(t *testing.T) {
	newTestRuntime().testLineEditorSelectionAndClipboard(t)
}
func TestLineEditorView(t *testing.T) {
	newTestRuntime().testLineEditorView(t)
}
func TestLineEditorFirstCharAfterLongLine(t *testing.T) {
	newTestRuntime().testLineEditorFirstCharAfterLongLine(t)
}
func TestConnectedDevicesFirst(t *testing.T) {
	newTestRuntime().testConnectedDevicesFirst(t)
}
func TestClearShowsWelcomeWithScreensOpen(t *testing.T) {
	newTestRuntime().testClearShowsWelcomeWithScreensOpen(t)
}
func TestMonochromeOnLightAndDarkTerminals(t *testing.T) {
	newTestRuntime().testMonochromeOnLightAndDarkTerminals(t)
}
