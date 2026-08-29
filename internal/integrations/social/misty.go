package social

import "context"

type MistyAdapter struct{}

func (MistyAdapter) Provider() SocialProviderID { return SocialProviderMisty }
func (MistyAdapter) Capabilities() SocialCapabilitySet {
	return SocialCapabilitySet{Read: true, Send: true}
}
func (MistyAdapter) DiscoverResources(context.Context, string) ([]SocialResource, error) {
	return nil, ErrUnsupportedOperation
}
func (MistyAdapter) NormalizeEvent(context.Context, []byte) ([]SocialMessage, error) {
	return nil, ErrUnsupportedOperation
}
func (MistyAdapter) Send(context.Context, string, SocialOutboundCommand) (SocialSendReceipt, error) {
	return SocialSendReceipt{}, ErrUnsupportedOperation
}
