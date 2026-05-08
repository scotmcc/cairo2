package db

import "fmt"

// ResolveModel returns the model to use for a given role.
// Resolution order: role.model → config.model → fallback (if non-empty).
// Returns an error if no model is found anywhere — callers must configure
// a model via `config(set, model=...)` or per-role via the role tool.
func ResolveModel(database *DB, roleName, fallback string) (string, error) {
	// 1. role-specific model
	if roleName != "" {
		roleModel, err := database.Roles.ModelFor(roleName)
		if err != nil {
			return "", err
		}
		if roleModel != "" {
			return roleModel, nil
		}
	}

	// 2. global config model
	configModel, err := database.Config.Get(KeyModel)
	if err != nil {
		return "", err
	}
	if configModel != "" {
		return configModel, nil
	}

	// 3. explicit fallback (should only be set during first-run wizard)
	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("no model configured — run the wizard or set one with: config(action=\"set\", key=\"model\", value=\"<model-name>\")")
}

// ResolveModelWithExplicit returns the model to use, checking an explicit override first.
// Resolution order: explicit → role.model → config.model → fallback.
// If explicit is empty, falls through to the normal ResolveModel chain.
func ResolveModelWithExplicit(database *DB, explicit, roleName, fallback string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return ResolveModel(database, roleName, fallback)
}
