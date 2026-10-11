package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
	"io"
	"math/big"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// Verification purposes are a closed set: callers cannot repurpose credentials.
const (
	EmailVerify   = "email_verify"
	SetPassword   = "set_password"
	PasswordReset = "password_reset"
	EmailChange   = "email_change"
	PhoneBind     = "phone_bind"
	PhoneLogin    = "phone_login"
	PhoneReset    = "phone_reset"
)

// VerificationCredential contains only a digest, never a bearer token or code.
// IssuedVersion is the target account snapshot. IssuerVersion is the original
// authenticated actor snapshot; an administrator may issue activation/reset.
type VerificationCredential struct {
	ID, UserID, IssuerUserID, Purpose, Target, Hash string
	IssuedVersion, IssuerVersion                    int64
	ExpiresAt                                       time.Time
	Attempts                                        int
	Accepted                                        bool
	Receipt                                         notification.Receipt
	UsedAt                                          *time.Time
}

// VerificationAttempt requests a fixed, purpose-specific atomic mutation.
// Authenticated identity changes require UserID and the original JWT version.
// PasswordHash must have been created by the service after strength validation.
type VerificationAttempt struct {
	Purpose, Target, Hash, UserID, PasswordHash string
	ExpectedVersion                             *int64
}

type VerificationLimits struct {
	Interval, Window     time.Duration
	TargetLimit, IPLimit int
}

func (v VerificationLimits) defaults() VerificationLimits {
	if v.Interval <= 0 {
		v.Interval = sendWindow
	}
	if v.Window <= 0 {
		v.Window = time.Hour
	}
	if v.TargetLimit <= 0 {
		v.TargetLimit = 5
	}
	if v.IPLimit <= 0 {
		v.IPLimit = 20
	}
	return v
}
func (s *Service) SetVerificationLimits(v VerificationLimits) { s.verificationLimits = v.defaults() }

func verificationSMS(purpose string) bool {
	return purpose == PhoneBind || purpose == PhoneLogin || purpose == PhoneReset
}
func verificationTarget(purpose, target string) (string, error) {
	switch purpose {
	case EmailVerify, SetPassword, PasswordReset, EmailChange:
		target = strings.ToLower(strings.TrimSpace(target))
		a, err := mail.ParseAddress(target)
		if err != nil || a.Address != target || len(target) > 254 {
			return "", pkgerrors.ErrConflict
		}
	case PhoneBind, PhoneLogin, PhoneReset:
		target = strings.TrimSpace(target)
		if !chinaPhoneRe.MatchString(target) {
			return "", pkgerrors.ErrConflict
		}
	default:
		return "", pkgerrors.ErrConflict
	}
	return target, nil
}
func verificationTTL(purpose string) time.Duration {
	if verificationSMS(purpose) {
		return smsCodeTTL
	}
	if purpose == EmailVerify {
		return emailTokenTTL
	}
	return 30 * time.Minute
}
func verificationInvalid() error {
	return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired verification credential")
}
func verificationHash(secret, purpose, target, value string) string {
	if verificationSMS(purpose) {
		h := hmac.New(sha256.New, []byte(secret))
		_, _ = h.Write([]byte("yuqing-verification-v1\x00" + purpose + "\x00" + target + "\x00" + value))
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func secureVerificationValue(sms bool) (string, error) {
	if sms {
		n, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			return "", pkgerrors.ErrServiceUnavailable
		}
		return fmt.Sprintf("%06d", n.Int64()), nil
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func validVerificationOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return "", pkgerrors.Wrap(pkgerrors.ErrServiceUnavailable, "public verification origin is not configured")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func verificationTemplate(purpose string) string {
	switch purpose {
	case PhoneBind:
		return "SMS_BIND_PHONE"
	case PhoneLogin:
		return "SMS_PHONE_LOGIN"
	case PhoneReset:
		return "SMS_PHONE_RESET"
	}
	return ""
}

// VerificationChannelChecker lets dynamic adapters report global configuration
// failure before a public target lookup, with identical known/unknown behavior.
type VerificationChannelChecker interface {
	CheckVerification(context.Context, string) error
}

func (s *Service) verificationReady(ctx context.Context, purpose string) error {
	if s.userStore == nil || s.verifications == nil || s.secret == "" {
		return pkgerrors.ErrServiceUnavailable
	}
	var channel any = s.emailSender
	if verificationSMS(purpose) {
		if s.smsSender == nil {
			return pkgerrors.ErrServiceUnavailable
		}
		channel = s.smsSender
	} else {
		if s.emailSender == nil {
			return pkgerrors.ErrServiceUnavailable
		}
		if _, err := validVerificationOrigin(s.verifyBaseURL); err != nil {
			return err
		}
	}
	if checker, ok := channel.(VerificationChannelChecker); ok {
		if err := checker.CheckVerification(ctx, purpose); err != nil {
			return pkgerrors.ErrServiceUnavailable
		}
	}
	return nil
}
func (s *Service) verificationUser(ctx context.Context, userID string, versions []int64) (*User, error) {
	if err := s.requireDeps(); err != nil {
		return nil, err
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, pkgerrors.ErrUnauthorized
	}
	if len(versions) > 0 && u.TokenVersion != versions[0] {
		return nil, pkgerrors.ErrUnauthorized
	}
	return u, nil
}
func (s *Service) issueVerification(ctx context.Context, u *User, purpose, target string) error {
	return s.issueVerificationAs(ctx, u, purpose, target, Principal{UserID: u.ID, TokenVersion: u.TokenVersion})
}

// SendAccountVerification is the K9 sender port, after account creation. Store
// issuance rechecks the original administrator version and platform role.
func (s *Service) SendAccountVerification(ctx context.Context, actor Principal, userID, purpose string) error {
	if purpose != SetPassword && purpose != PasswordReset {
		return pkgerrors.ErrConflict
	}
	if err := s.verificationReady(ctx, purpose); err != nil {
		return err
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return err
	}
	target, err := verificationTarget(purpose, u.Email)
	if err != nil {
		return err
	}
	if err = s.verifications.ReserveSend(ctx, purpose, target, "", s.verificationLimits); err != nil {
		return err
	}
	return s.issueVerificationAs(ctx, u, purpose, target, actor)
}
func (s *Service) issueVerificationAs(ctx context.Context, u *User, purpose, target string, actor Principal) error {
	value, err := secureVerificationValue(verificationSMS(purpose))
	if err != nil {
		return err
	}
	c := VerificationCredential{ID: id.New(), UserID: u.ID, IssuerUserID: actor.UserID, Purpose: purpose, Target: target, IssuedVersion: u.TokenVersion, IssuerVersion: actor.TokenVersion, Hash: verificationHash(s.secret, purpose, target, value), ExpiresAt: time.Now().Add(verificationTTL(purpose))}
	if err = s.verifications.Issue(ctx, c); err != nil {
		return err
	}
	receipt := notification.Receipt{Provider: "custom", State: "rejected"}
	if verificationSMS(purpose) {
		if sender, ok := s.smsSender.(interface {
			SendReceipt(context.Context, string, string, map[string]string) (notification.Receipt, error)
		}); ok {
			receipt, err = sender.SendReceipt(ctx, target, verificationTemplate(purpose), map[string]string{"code": value})
		} else {
			err = s.smsSender.Send(ctx, target, verificationTemplate(purpose), map[string]string{"code": value})
		}
	} else {
		origin, _ := validVerificationOrigin(s.verifyBaseURL)
		path := "/verify-email"
		if purpose == PasswordReset {
			path = "/reset-password"
		}
		if purpose == EmailChange {
			path = "/email-change"
		}
		if purpose == SetPassword {
			path = "/activate"
		}
		link := origin + path + "#token=" + url.QueryEscape(value)
		body := buildVerifyEmailHTML(u.Name, link)
		if purpose != EmailVerify {
			body = "<p>请在 30 分钟内打开安全链接完成操作：</p><p><a href=\"" + link + "\">完成验证</a></p>"
		}
		subjects := map[string]string{EmailVerify: "验证邮箱", PasswordReset: "重置密码", EmailChange: "确认更换邮箱", SetPassword: "激活账户并设置密码"}
		subject := subjects[purpose] + " - 盘古舆情"
		if sender, ok := s.emailSender.(interface {
			SendReceipt(context.Context, string, string, string) (notification.Receipt, error)
		}); ok {
			receipt, err = sender.SendReceipt(ctx, target, subject, body)
		} else {
			err = s.emailSender.Send(ctx, target, subject, body)
		}
	}
	accepted := err == nil
	if accepted && receipt.Provider == "custom" {
		receipt.State = "accepted"
		receipt.AcceptedAt = time.Now().UTC()
	}
	if !accepted {
		provider := receipt.Provider
		if provider == "" {
			provider = "custom"
		}
		receipt = notification.Receipt{Provider: provider, State: "rejected"}
	}
	var persistErr error
	if store, ok := s.verifications.(interface {
		RecordReceipt(context.Context, string, notification.Receipt) error
	}); ok {
		persistErr = store.RecordReceipt(ctx, c.ID, receipt)
	} else {
		persistErr = s.verifications.RecordDelivery(ctx, c.ID, accepted)
	}
	if persistErr != nil {
		return persistErr
	}
	if !accepted {
		return pkgerrors.Wrap(pkgerrors.ErrServiceUnavailable, "verification delivery was not accepted")
	}
	return nil
}

// RequestPublicVerification persists gates before existence lookup. An accepted
// request is not a supplier receipt; unknown targets and rejected sends match.
func (s *Service) RequestPublicVerification(ctx context.Context, purpose, target, ip string) error {
	if purpose != PasswordReset && purpose != PhoneLogin && purpose != PhoneReset {
		return pkgerrors.ErrConflict
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return pkgerrors.ErrConflict
	}
	ip = parsedIP.String()
	target, err := verificationTarget(purpose, target)
	if err != nil {
		return err
	}
	if err = s.verificationReady(ctx, purpose); err != nil {
		return err
	}
	if err = s.verifications.ReserveSend(ctx, purpose, target, ip, s.verificationLimits); err != nil {
		return err
	}
	var u *User
	if verificationSMS(purpose) {
		u, err = s.userByPhone(ctx, target)
	} else {
		u, err = s.store.GetUserByEmail(ctx, target)
	}
	if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		return nil
	}
	if err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	if u.Status != "active" || (verificationSMS(purpose) && u.PhoneVerifiedAt == nil) {
		return nil
	}
	// Supplier acceptance is recorded durably. Its per-target outcome remains
	// private to avoid enumeration through delivery/configuration differences.
	_ = s.issueVerification(ctx, u, purpose, target)
	return nil
}

// ConfirmVerification is the frozen K7/K9 consumption port. It never returns
// tokens or grants credit. PasswordReset/PhoneReset/SetPassword require one
// canonical trusted socket IP; missing source cannot bypass shared hash admission.
func (s *Service) ConfirmVerification(ctx context.Context, purpose, target, value, newPassword string, actor *Principal, sourceIP ...string) (*User, error) {
	if s.verifications == nil || s.secret == "" {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	if verificationSMS(purpose) || target != "" {
		var err error
		target, err = verificationTarget(purpose, target)
		if err != nil {
			return nil, err
		}
	} else {
		switch purpose {
		case EmailVerify, SetPassword, PasswordReset, EmailChange:
		default:
			return nil, pkgerrors.ErrConflict
		}
	}
	a := VerificationAttempt{Purpose: purpose, Target: target, Hash: verificationHash(s.secret, purpose, target, value)}
	if value == "" || len(value) > 256 {
		return nil, verificationInvalid()
	}
	if actor != nil {
		a.UserID = actor.UserID
		a.ExpectedVersion = &actor.TokenVersion
	}
	if passwordConfirmationPurpose(purpose) {
		if !isStrongPassword(newPassword) {
			return nil, pkgerrors.Wrap(pkgerrors.ErrConflict, "password must be 8+ characters with letters and digits")
		}
		release, err := s.beginPasswordConfirmation(ctx, a, sourceIP)
		if err != nil {
			return nil, err
		}
		defer release()
		hash, err := HashPassword(newPassword)
		if err != nil {
			return nil, pkgerrors.ErrServiceUnavailable
		}
		if ctx.Err() != nil {
			return nil, pkgerrors.ErrServiceUnavailable
		}
		a.PasswordHash = hash
	}
	result, err := s.verifications.Consume(ctx, a)
	// Public confirmation must not reveal whether a credential existed through
	// the bind flow's special fifth-failure response. Durable invalidation stays.
	if (purpose == PhoneLogin || purpose == PhoneReset) && pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		return nil, verificationInvalid()
	}
	return result, err
}

func validateVerificationCredential(c VerificationCredential) error {
	target, err := verificationTarget(c.Purpose, c.Target)
	if err != nil || target != c.Target || c.ID == "" || c.UserID == "" || c.IssuerUserID == "" || c.IssuedVersion < 0 || c.IssuerVersion < 0 || len(c.Hash) != 64 || c.ExpiresAt.IsZero() || c.Attempts != 0 || c.Accepted || c.UsedAt != nil {
		return pkgerrors.ErrConflict
	}
	if _, err := hex.DecodeString(c.Hash); err != nil {
		return pkgerrors.ErrConflict
	}
	return nil
}
func verificationUserMatches(u *User, c VerificationCredential) bool {
	if u == nil || u.TokenVersion != c.IssuedVersion {
		return false
	}
	if c.Purpose == SetPassword {
		return u.Status == "pending_activation" && strings.EqualFold(u.Email, c.Target)
	}
	if u.Status != "active" {
		return false
	}
	switch c.Purpose {
	case EmailVerify, PasswordReset:
		return strings.EqualFold(u.Email, c.Target)
	case PhoneLogin, PhoneReset:
		return u.Phone == c.Target && u.PhoneVerifiedAt != nil
	}
	return true
}
func applyVerification(u *User, c VerificationCredential, a VerificationAttempt, now time.Time) error {
	if !verificationUserMatches(u, c) || !c.Accepted || c.UsedAt != nil || !now.Before(c.ExpiresAt) || c.Attempts >= maxCodeTries {
		return verificationInvalid()
	}
	if a.UserID != "" && a.UserID != u.ID {
		return verificationInvalid()
	}
	if a.ExpectedVersion != nil && *a.ExpectedVersion != u.TokenVersion {
		return verificationInvalid()
	}
	if c.Purpose == PhoneBind || c.Purpose == EmailChange {
		if a.UserID != u.ID || a.ExpectedVersion == nil {
			return verificationInvalid()
		}
	}
	if a.Target != "" && a.Target != c.Target {
		return verificationInvalid()
	}
	if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(a.Hash)) != 1 {
		return verificationInvalid()
	}
	switch c.Purpose {
	case EmailVerify:
		if u.EmailVerifiedAt != nil {
			return verificationInvalid()
		}
		u.EmailVerifiedAt = &now
		u.RowVersion++
	case PhoneBind:
		u.Phone = c.Target
		u.PhoneVerifiedAt = &now
		u.TokenVersion++
		u.RowVersion++
	case EmailChange:
		u.Email = c.Target
		u.EmailVerifiedAt = &now
		u.TokenVersion++
		u.RowVersion++
	case PasswordReset, PhoneReset, SetPassword:
		if !strings.HasPrefix(a.PasswordHash, "$argon2id$") {
			return pkgerrors.ErrConflict
		}
		u.PasswordHash = a.PasswordHash
		u.PasswordChangedAt = &now
		u.TokenVersion++
		u.RowVersion++
		if c.Purpose == SetPassword {
			u.Status = "active"
			u.EmailVerifiedAt = &now
		}
	case PhoneLogin:
	default:
		return verificationInvalid()
	}
	return nil
}

// LoginWithPhoneCode is the K7 login consumer port. Authorization and signing
// use the exact version returned by atomic consumption; never upgrade it after
// a concurrent password/identity change. Routes are mounted by K7.
func (s *Service) LoginWithPhoneCode(ctx context.Context, phone, code string) (*Principal, *TokenPair, error) {
	u, err := s.ConfirmVerification(ctx, PhoneLogin, phone, code, "", nil)
	if err != nil {
		return nil, nil, err
	}
	tenantID := ""
	tenant, err := s.store.GetUserTenant(ctx, u.ID)
	if err != nil && !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		return nil, nil, err
	}
	if tenant != nil {
		tenantID = tenant.ID
	}
	principal, err := s.loadPrincipal(ctx, u.ID, tenantID, u.TokenVersion)
	if err != nil {
		return nil, nil, err
	}
	recorder, ok := s.store.(LoginRecorder)
	if !ok {
		return nil, nil, pkgerrors.ErrServiceUnavailable
	}
	if err = recorder.RecordSuccessfulLogin(ctx, u.ID, u.TokenVersion); err != nil {
		return nil, nil, err
	}
	pair, err := GenerateTokenPair(*principal, s.secret, s.accessTTL, s.refreshTTL)
	if err != nil {
		return nil, nil, err
	}
	return principal, pair, nil
}
