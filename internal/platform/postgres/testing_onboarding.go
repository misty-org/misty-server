package db

func TestingOnboardingFingerprint(name string, apps []AppInstallSpec) (string, error) {
	return onboardingFingerprint(name, apps)
}
