package tools

import "strings"

// PolicyFor returns the declared tool policy, or a conservative compatibility
// policy for tools that have not migrated to ToolPolicyProvider yet.
func PolicyFor(tool Tool) ToolPolicy {
	if provider, ok := tool.(ToolPolicyProvider); ok {
		return provider.Policy()
	}
	return compatibilityPolicy(tool.Name())
}

// PolicyForName is the fallback used when a tool is no longer callable, for
// example an expired discovered MCP tool.
func PolicyForName(name string) ToolPolicy {
	return compatibilityPolicy(name)
}

func compatibilityPolicy(name string) ToolPolicy {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_search", "web_fetch", "fetch":
		return RetrievalPolicy()
	case "read_file", "list_dir", "load_image", "find_skills", "memory_search", "short_grep", "short_expand":
		return ReadPolicy()
	case "tool_search":
		policy := ReadPolicy()
		policy.Idempotent = false
		return policy
	default:
		return StatePolicy()
	}
}
