package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Account state powers the management panel: balance, active token packages,
// and usage statistics.

// Package is one active token package.
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

	Packages []Package `json:"packages"`

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
