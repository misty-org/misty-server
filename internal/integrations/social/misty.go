package social

type MistyAdapter struct{}

func (MistyAdapter) Capabilities() SocialCapabilitySet {
	return SocialCapabilitySet{Read: true, Send: true}
}
