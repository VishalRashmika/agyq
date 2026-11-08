package application

import "fmt"

// DefaultStarterQueue is the starter markdown queue created by 'agyq init'.
const DefaultStarterQueue = `# Queue for agyq sequential execution

# #1 Scaffold starter prompt
Add your task description here. agyq will execute prompts sequentially.

# #2 Second prompt
Subsequent prompts are executed one-by-one with automatic checkpoints.
`

// DefaultAgentsMD provides standard guidelines for autonomous coding agents so they always apply code changes directly.
const DefaultAgentsMD = `# Autonomous Coding Worker Guidelines

1. You are running as an autonomous coding agent via agyq.
2. ALWAYS implement the requested changes directly in the workspace files using your editing tools (write_file, edit_file).
3. Do not just explain how to solve the problem in text; apply the code changes directly.
4. Verify your changes by running tests or build commands before concluding.
`

// DefaultAgyqConfigJSON returns a clean JSON configuration with recommended defaults.
func DefaultAgyqConfigJSON(permissionMode string) string {
	if permissionMode == "" {
		permissionMode = "dangerously-skip-permissions"
	}
	return fmt.Sprintf(`{
  "recovery": "resume",
  "on_failure": "retry-at-end",
  "prompt_timeout": "90m",
  "agy": {
    "binary": "agy",
    "model": "gemini-3.8-flash-high",
    "effort": "high",
    "permission_mode": %q
  }
}
`, permissionMode)
}
