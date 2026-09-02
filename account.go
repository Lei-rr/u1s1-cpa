package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Account state powers the management panel: balance, active token packages,
// check-in status, and claimable rewards.

// Package is one active or staged token package.
type Package struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	Scope       string  `json:"scope"`
	DailyTokens *int64  `json:"daily_tokens"`
	TotalTokens *int64  `json:"total_tokens"`
	UsedToday   int64   `json:"used_today"`
	UsedTokens  int64   `json:"used_tokens"`
	Remaining   int64   `json:"remaining"`
	ExpiresAt   *string `json:"expires_at"`
	Note        *string `json:"note"`
	CreatedAt   *string `json:"created_at"`
}

// Limit returns the package ceiling and whether it is a daily allowance.
func (p Package) Limit() (int64, bool) {
	if p.DailyTokens != nil {
		return *p.DailyTokens, true
	}
	if p.TotalTokens != nil {
		return *p.TotalTokens, false
	}
	return 0, false
}

// PackageLabels maps package kind codes to human-readable names.
var PackageLabels = map[string]string{
	"free_first":          "首月免费包",
	"free_yearly":         "年度免费包",
	"new_user":            "新用户赠送包",
	"invite":              "邀请赠送包",
	"login_checkin":       "登录打卡",
	"login_checkin_bonus": "连续打卡加成",
	"payment_delay_gift":  "临时加量包",
	"topup_daily":         "每日加量包",
	"admin_grant":         "官方赠送",
}

// StagedGrant represents a pending or held staged reward package waiting to be unlocked.
type StagedGrant struct {
	ID             int64   `json:"id"`
	SourceKind     string  `json:"source_kind"` // "new_user" | "invite"
	State          string  `json:"state"`       // "pending" | "held" | "released"
	TotalTokens    int64   `json:"total_tokens"`
	ReleasedTokens int64   `json:"released_tokens"`
	ActiveDays     int64   `json:"active_days"`
	Requests       int64   `json:"requests"`
	OutputTokens   int64   `json:"output_tokens"`
	CreatedAt      *string `json:"created_at"`
	Requirements   *struct {
		ActiveDays   int64 `json:"active_days"`
		Requests     int64 `json:"requests"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"requirements"`
}

// Account is the decoded /v1/me response.
type Account struct {
	Email string `json:"email"`

	DailyFreeUSD          float64 `json:"daily_free_usd"`
	DailyFreeUsedUSD      float64 `json:"daily_free_used_usd"`
	DailyFreeRemainingUSD float64 `json:"daily_free_remaining_usd"`
	DailyFreeResetsAt     string  `json:"daily_free_resets_at"`
	DailyFreeModel        string  `json:"daily_free_model"`

	MtdUSD          float64 `json:"mtd_usd"`
	BalanceSpentUSD float64 `json:"balance_spent_usd"`
	BonusBalanceUSD float64 `json:"bonus_balance_usd"`
	RemainingUSD    float64 `json:"remaining_usd"`
	TokensPerUSD    int64   `json:"tokens_per_usd"`

	Packages     []Package     `json:"packages"`
	StagedGrants []StagedGrant `json:"staged_grants"`

	// FreeClaim is "first", "renew", or null when a free package can be claimed.
	FreeClaim *string `json:"free_claim"`
	// InviteClaim and NewUserClaim are "available" when a reward is claimable.
	InviteClaim  string `json:"invite_claim"`
	NewUserClaim string `json:"new_user_claim"`
	// Blocked reasons explain why a reward cannot be claimed yet.
	InviteClaimBlockedReason  string `json:"invite_claim_blocked_reason"`
	NewUserClaimBlockedReason string `json:"new_user_claim_blocked_reason"`
	ClaimsPaused              bool   `json:"claims_paused"`

	// Checkin is present on gateways that expose daily check-in state.
	Checkin *struct {
		ClaimedToday  bool  `json:"claimed_today"`
		Streak        int64 `json:"streak"`
		LongestStreak int64 `json:"longest_streak"`
	} `json:"checkin"`

	// FetchedAt records when this snapshot was taken.
	FetchedAt time.Time `json:"fetched_at"`
}

// RemainingPackageTokens sums the token balance across active packages.
func (a Account) RemainingPackageTokens() int64 {
	var total int64
	for _, p := range a.Packages {
		total += p.Remaining
	}
	return total
}

// quotaEntry is a cached account snapshot.
type quotaEntry struct {
	account Account
	fetched time.Time
}

var (
	quotaMu    sync.RWMutex
	quotaCache = map[string]quotaEntry{}
)

// quotaTTL bounds how often the panel hits the gateway. The panel refreshes on
// open, and several accounts may render at once, so a short TTL keeps a page
// load to one upstream call per credential.
const quotaTTL = 30 * time.Second

// fetchAccount reads /v1/me for a credential, honouring the snapshot cache.
func fetchAccount(callbackID string, s Storage) (Account, error) {
	quotaMu.RLock()
	cached, ok := quotaCache[s.DeviceToken]
	quotaMu.RUnlock()
	if ok && time.Since(cached.fetched) < quotaTTL {
		return cached.account, nil
	}

	var account Account
	if err := doJSON(callbackID, s, "GET", s.APIBase()+"/v1/me", nil, &account); err != nil {
		return Account{}, err
	}
	account.FetchedAt = time.Now()

	quotaMu.Lock()
	quotaCache[s.DeviceToken] = quotaEntry{account: account, fetched: time.Now()}
	quotaMu.Unlock()
	return account, nil
}

// invalidateAccounts drops cached snapshots so the next read is live.
func invalidateAccounts() {
	quotaMu.Lock()
	quotaCache = map[string]quotaEntry{}
	quotaMu.Unlock()
}

// --- rewards and check-in ---

// Reward endpoints live on the u1s1 website, not the inference gateway, and are
// protected by a browser session plus a CAPTCHA (Capcat + Cloudflare Turnstile).
// A device credential cannot authenticate them: the site returns
// {"error":{"message":"not logged in"}} for DPoP-signed requests, verified
// against /api/me, /api/packages/login-checkin/claim, and
// /api/packages/new-user/claim.
//
// The plugin therefore reports claim state and links to the dashboard rather
// than attempting to solve the CAPTCHA. Automating it would require running the
// challenge's instrumentation payload in a browser environment, which cannot be
// done from an in-process CPA plugin, and would amount to circumventing an
// anti-abuse control the operator put in place.

// ClaimKind identifies a claimable reward.
type ClaimKind string

const (
	ClaimCheckin ClaimKind = "login-checkin"
	ClaimNewUser ClaimKind = "new-user"
	ClaimInvite  ClaimKind = "invite"
)

// Claim describes one reward and whether it can currently be collected.
type Claim struct {
	Kind      ClaimKind `json:"kind"`
	Label     string    `json:"label"`
	Available bool      `json:"available"`
	Detail    string    `json:"detail"`
	// URL is where the user completes the claim.
	URL string `json:"url"`
}

// dashboardURL is where reward claims are completed.
func dashboardURL(s Storage) string { return s.WebBase() + "/dashboard" }

// hasTodayLoginCheckinPackage checks if the account already has a login_checkin package granted today (Asia/Shanghai).
func hasTodayLoginCheckinPackage(packages []Package) (bool, string) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	if shanghai == nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}
	todayStr := time.Now().In(shanghai).Format("2006-01-02")
	for _, p := range packages {
		if p.Kind == "login_checkin" && p.CreatedAt != nil {
			if strings.HasPrefix(*p.CreatedAt, todayStr) {
				return true, *p.CreatedAt
			}
		}
	}
	return false, ""
}

// countLoginCheckinPackages counts how many check-in gifts this account has received.
func countLoginCheckinPackages(packages []Package) int {
	cnt := 0
	for _, p := range packages {
		if p.Kind == "login_checkin" {
			cnt++
		}
	}
	return cnt
}

// claims derives the reward list for an account snapshot.
func claims(s Storage, a Account) []Claim {
	out := make([]Claim, 0, 4)
	url := dashboardURL(s)

	claimedToday, lastClaimAt := hasTodayLoginCheckinPackage(a.Packages)
	checkinCount := countLoginCheckinPackages(a.Packages)

	checkin := Claim{Kind: ClaimCheckin, Label: "每日打卡 (200万 Token)", URL: url}
	if a.Checkin != nil {
		if a.Checkin.ClaimedToday || claimedToday {
			checkin.Detail = fmt.Sprintf("今日已打卡 ✓ · 连续 %d 天（历史累计 %d 次）", a.Checkin.Streak, checkinCount)
		} else {
			checkin.Available = true
			checkin.Detail = fmt.Sprintf("今日待打卡 · 连续 %d 天 · 点击直达领取", a.Checkin.Streak)
		}
	} else {
		// Inferred from login_checkin package list
		if claimedToday {
			checkin.Detail = fmt.Sprintf("今日已打卡 ✓ (发放时间 %s · 累计 %d 次)", lastClaimAt, checkinCount)
		} else {
			checkin.Available = true
			checkin.Detail = fmt.Sprintf("今日待打卡 (累计已打卡 %d 次) · 点击直达领取", checkinCount)
		}
	}
	out = append(out, checkin)

	if a.NewUserClaim != "" {
		newUser := Claim{Kind: ClaimNewUser, Label: "新用户礼包 (500万/1000万 Token)", URL: url}
		newUser.Available = strings.EqualFold(a.NewUserClaim, "available")
		newUser.Detail = claimDetail(newUser.Available, a.NewUserClaimBlockedReason, a.ClaimsPaused)
		out = append(out, newUser)
	}

	if a.InviteClaim != "" {
		invite := Claim{Kind: ClaimInvite, Label: "邀请好友赠送 (最高1000万 Token)", URL: url}
		invite.Available = strings.EqualFold(a.InviteClaim, "available")
		invite.Detail = claimDetail(invite.Available, a.InviteClaimBlockedReason, a.ClaimsPaused)
		out = append(out, invite)
	}

	if a.FreeClaim != nil && *a.FreeClaim != "" {
		label := "首月免费用量包 (每天200万 Token)"
		if *a.FreeClaim == "renew" {
			label = "年度免费用量包 (每天100万 Token)"
		}
		out = append(out, Claim{
			Kind:      "free",
			Label:     label,
			Available: true,
			Detail:    "可领取/可续期 · 前往面板激活",
			URL:       url,
		})
	}
	return out
}

// claimDetail explains a reward's current state.
func claimDetail(available bool, blockedReason string, paused bool) string {
	if available {
		return "可领取，前往面板完成人机验证"
	}
	switch blockedReason {
	case "phone_required":
		return "需先在面板绑定并验证手机号"
	case "phone_reused":
		return "手机号有跨账号绑定历史，按防滥用规则不可领取"
	case "inviter_phone_required":
		return "邀请人手机号未验证"
	}
	if paused {
		return "当前暂停领取"
	}
	return "已领取或不适用"
}

// decode unmarshals an ABI request payload.
func decode[T any](payload []byte) (T, error) {
	var out T
	if len(payload) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, fmt.Errorf("decode request: %w", err)
	}
	return out, nil
}
