package tgminiapp

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"tg-podcastotron/auth"
	"tg-podcastotron/service"
)

// gcConfirmTTL is how long the admin can confirm a storage scan.
const gcConfirmTTL = time.Hour

func requireAdmin(u *User) error {
	if !u.IsAdmin {
		return &userError{http.StatusForbidden, "Only the admin can do this."}
	}
	return nil
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireAdmin(u); err != nil {
		return err
	}
	return s.render(w, "admin", nil)
}

type inviteData struct {
	Link    string
	Expires time.Duration
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireAdmin(u); err != nil {
		return err
	}
	invite, err := s.auth.CreateInvite(r.Context(), u.ID)
	if err != nil {
		return err
	}
	return s.partial(w, "invite", &inviteData{
		Link:    InviteLink(s.cfg.BotUsername, invite),
		Expires: auth.InviteMaxAge,
	})
}

// InvitePrefix is the prefix of the /start parameter of an invite link.
const InvitePrefix = "inv_"

// InviteLink returns the t.me link that opens the bot with the invite.
func InviteLink(botUsername string, invite *auth.Invite) string {
	return "https://t.me/" + botUsername + "?start=" + InvitePrefix + invite.Token
}

type gcReportData struct {
	Report *service.GCReport
	Token  string
	Grace  time.Duration
}

func (s *Server) gcScan(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireAdmin(u); err != nil {
		return err
	}
	report, err := s.svc.ScanStorageGarbage(r.Context())
	if err != nil {
		return err
	}
	data := &gcReportData{Report: report, Grace: service.GCGracePeriod}
	if len(report.Garbage) > 0 {
		keys := make([]string, len(report.Garbage))
		for i, obj := range report.Garbage {
			keys[i] = obj.Key
		}
		data.Token = s.gcScans.put(keys)
	}
	return s.partial(w, "gc-report", data)
}

type gcResultData struct {
	Expired bool
	Deleted int
	Total   int
	Bytes   int64
}

func (s *Server) gcDelete(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireAdmin(u); err != nil {
		return err
	}
	keys, ok := s.gcScans.take(r.FormValue("token"))
	if !ok {
		return s.partial(w, "gc-result", &gcResultData{Expired: true})
	}
	deleted, bytes, err := s.svc.DeleteStorageGarbage(r.Context(), keys)
	if err != nil {
		return err
	}
	return s.partial(w, "gc-result", &gcResultData{Deleted: deleted, Total: len(keys), Bytes: bytes})
}

// gcPendingScans remembers scans that wait for the confirmation of the admin, so that
// the confirmation deletes only the files that the admin saw (less the files that
// stopped being garbage since then).
type gcPendingScans struct {
	mu    sync.Mutex
	scans map[string]gcPendingScan
}

type gcPendingScan struct {
	keys      []string
	expiresAt time.Time
}

func (p *gcPendingScans) put(keys []string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scans == nil {
		p.scans = make(map[string]gcPendingScan)
	}
	for token, scan := range p.scans {
		if time.Now().After(scan.expiresAt) {
			delete(p.scans, token)
		}
	}
	token := uuid.NewString()
	p.scans[token] = gcPendingScan{keys: keys, expiresAt: time.Now().Add(gcConfirmTTL)}
	return token
}

// take removes and returns the scan for token, if it exists and did not expire.
func (p *gcPendingScans) take(token string) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	scan, ok := p.scans[token]
	delete(p.scans, token)
	if !ok || time.Now().After(scan.expiresAt) {
		return nil, false
	}
	return scan.keys, true
}
