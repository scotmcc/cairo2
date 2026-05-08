// Progress and context improvements for Cairo VS Code Extension
//
// This implementation adds:
// 1. Real-time progress indicators during Cairo execution
// 2. Live file context tracking (showing modified files in real-time)
// 3. Error recovery guidance with "Here's what to try"
// 4. Task completion summaries showing what was accomplished
// 5. Context-aware command suggestions based on open files

// Key features:
// - File context panel: Shows live file operations (created/modified/deleted)
// - Task summary view: Displays completion status and result summary
// - Error recovery panel: Diagnoses errors and suggests recovery actions
// - Progress bar: Visual indicator of task progress
// - Context-aware suggestions: Recommends commands based on open files

// Integration points:
// - Hook into Cairo's output to detect file operations
// - Parse error messages for pattern matching
// - Update webviews with real-time status
// - Auto-generate summaries when tasks complete

// Usage:
// 1. Open File Context panel: Ctrl+Shift+P → Cairo: Open File Context
// 2. View task summaries in dedicated panel
// 3. Error recovery automatically shown on failures
// 4. Context suggestions appear based on open files
