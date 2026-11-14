package verbs

import "mc/routing"

type OnboardSandboxPreview = onboardSandboxProfile

// PreviewOnboardSandbox returns the generated access profile without a filesystem change.
func PreviewOnboardSandbox(a OnboardArgs) (OnboardSandboxPreview, error) {
	return generatedOnboardSandboxProfile(a)
}

// PreviewOnboardRouting validates the selected role assignments without a filesystem change.
func PreviewOnboardRouting(a OnboardArgs) (routing.Table, error) {
	registry, first, err := onboardRoutingRegistry(a)
	if err != nil {
		return routing.Table{}, err
	}
	if a.RoutingFile == "" {
		return routing.UniformTable(first, registry)
	}
	body, err := readOnboardRoutingFile(a.RoutingFile)
	if err != nil {
		return routing.Table{}, err
	}
	return routing.Parse(body, registry)
}
