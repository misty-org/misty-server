package app

import (
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountSocialRoutes serves what belongs to the account rather than a Space: friends and blocks,
// DMs (each a Space of kind 'direct', whose messages use the Space chat routes by its id), status,
// one-time notices and call tokens. None of these paths sit under /me/conversations, which the
// no-agents contract keeps empty.
func (s *Server) mountSocialRoutes(prefix string, social *api.SocialService) {
	s.Router.Get(prefix+"/friends", social.Friends())
	s.Router.Post(prefix+"/friends/requests", social.FriendRequests())
	s.Router.Delete(prefix+"/friends/requests", social.FriendRequests())
	s.Router.Post(prefix+"/friends/requests/{userID}/accept", social.FriendRequest())
	s.Router.Delete(prefix+"/friends/requests/{userID}", social.FriendRequest())
	s.Router.Delete(prefix+"/friends/{userID}", social.Friend())
	s.Router.Put(prefix+"/blocks/{userID}", social.Block())
	s.Router.Delete(prefix+"/blocks/{userID}", social.Block())
	s.Router.Get(prefix+"/me/privacy", social.Privacy())
	s.Router.Put(prefix+"/me/privacy", social.Privacy())
	s.Router.Get(prefix+"/me/status", social.Status())
	s.Router.Put(prefix+"/me/status", social.Status())
	s.Router.Get(prefix+"/statuses", social.Statuses())
	s.Router.Get(prefix+"/me/notices", social.Notices())
	s.Router.Post(prefix+"/me/notices/{noticeID}/seen", social.Notices())
	s.Router.Get(prefix+"/dms", social.Directs())
	s.Router.Post(prefix+"/dms", social.Directs())
	s.Router.Get(prefix+"/dms/{dmID}", social.Direct())
	s.Router.Delete(prefix+"/dms/{dmID}", social.Direct())
	s.Router.Post(prefix+"/dms/{dmID}/members", social.DirectMembers())
	s.Router.Delete(prefix+"/dms/{dmID}/members/me", social.LeaveDirect())
	s.Router.Post(prefix+"/dms/{dmID}/call", social.DirectCall())
	s.Router.Get(prefix+"/calls/config", social.CallConfig())
	s.Router.Post(prefix+"/calls/{room}/token", social.CallToken())
}
