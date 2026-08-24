package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int  // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int  // 令牌额度实际扣减量
	extraReserved    int  // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	trusted          bool // 是否命中信任额度旁路
	fundingSettled   bool // funding.Settle 已成功，资金来源已提交
	settled          bool // Settle 全部完成（资金 + 令牌）
	refunded         bool // Refund 已调用
	mu               sync.Mutex
}

// Settle 根据实际消耗额度进行结算。
// 资金来源和令牌额度分两步提交：若资金来源已提交但令牌调整失败，
// 会标记 fundingSettled 防止 Refund 对已提交的资金来源执行退款。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delta := actualQuota - s.preConsumedQuota
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":           "settle_start",
		"request_id":      s.relayInfo.RequestId,
		"user_id":         s.relayInfo.UserId,
		"actual_quota":    actualQuota,
		"pre_consumed":    s.preConsumedQuota,
		"delta":           delta,
		"funding_source":  s.funding.Source(),
		"funding_settled": s.fundingSettled,
		"token_consumed":  s.tokenConsumed,
	})
	if s.settled {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":      "settle_skipped",
			"request_id": s.relayInfo.RequestId,
			"user_id":    s.relayInfo.UserId,
			"reason":     "already_settled",
		})
		return nil
	}
	if delta == 0 {
		s.settled = true
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":          "settle_done",
			"request_id":     s.relayInfo.RequestId,
			"user_id":        s.relayInfo.UserId,
			"funding_source": s.funding.Source(),
			"actual_quota":   actualQuota,
			"delta":          delta,
		})
		return nil
	}

	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":          "settle_funding",
			"request_id":     s.relayInfo.RequestId,
			"user_id":        s.relayInfo.UserId,
			"funding_source": s.funding.Source(),
			"delta":          delta,
		})
		if err := s.funding.Settle(delta); err != nil {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":          "settle_funding_error",
				"request_id":     s.relayInfo.RequestId,
				"user_id":        s.relayInfo.UserId,
				"funding_source": s.funding.Source(),
				"delta":          delta,
				"error":          err.Error(),
			})
			return err
		}
		s.fundingSettled = true
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":          "settle_funding_done",
			"request_id":     s.relayInfo.RequestId,
			"user_id":        s.relayInfo.UserId,
			"funding_source": s.funding.Source(),
			"delta":          delta,
		})
	}

	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		action := "increase"
		amount := -delta
		if delta > 0 {
			action = "decrease"
			amount = delta
		}
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":      "settle_token",
			"request_id": s.relayInfo.RequestId,
			"user_id":    s.relayInfo.UserId,
			"token_id":   s.relayInfo.TokenId,
			"action":     action,
			"amount":     amount,
		})
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":      "settle_token_error",
				"request_id": s.relayInfo.RequestId,
				"user_id":    s.relayInfo.UserId,
				"token_id":   s.relayInfo.TokenId,
				"action":     action,
				"amount":     amount,
				"error":      tokenErr.Error(),
			})
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		} else {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":      "settle_token_done",
				"request_id": s.relayInfo.RequestId,
				"user_id":    s.relayInfo.UserId,
				"token_id":   s.relayInfo.TokenId,
				"action":     action,
				"amount":     amount,
			})
		}
	} else {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":      "settle_token_skipped",
			"request_id": s.relayInfo.RequestId,
			"user_id":    s.relayInfo.UserId,
			"reason":     "playground",
		})
	}

	// 3) 更新 relayInfo 上的订阅 PostDelta（用于日志）
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled = true
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":          "settle_done",
		"request_id":     s.relayInfo.RequestId,
		"user_id":        s.relayInfo.UserId,
		"funding_source": s.funding.Source(),
		"actual_quota":   actualQuota,
		"delta":          delta,
		"token_error":    tokenErr != nil,
	})
	return tokenErr
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	needsRefund := s.needsRefundLocked()
	if s.settled || s.refunded || !needsRefund {
		reason := "no_refundable_preconsume"
		switch {
		case s.settled:
			reason = "already_settled"
		case s.refunded:
			reason = "already_refunded"
		case s.fundingSettled:
			reason = "funding_already_settled"
		}
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":           "refund_skipped",
			"request_id":      s.relayInfo.RequestId,
			"user_id":         s.relayInfo.UserId,
			"funding_source":  s.funding.Source(),
			"reason":          reason,
			"settled":         s.settled,
			"funding_settled": s.fundingSettled,
			"refunded":        s.refunded,
		})
		s.mu.Unlock()
		return
	}
	s.refunded = true

	// 在解锁前复制异步退款需要的全部状态，避免闭包读取可变会话字段。
	requestId := s.relayInfo.RequestId
	userId := s.relayInfo.UserId
	tokenId := s.relayInfo.TokenId
	tokenKey := s.relayInfo.TokenKey
	isPlayground := s.relayInfo.IsPlayground
	tokenConsumed := s.tokenConsumed
	preConsumed := s.preConsumedQuota
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	funding := s.funding
	fundingSource := funding.Source()

	logger.BillingDebugMap(c, map[string]interface{}{
		"stage":          "refund_start",
		"request_id":     requestId,
		"user_id":        userId,
		"funding_source": fundingSource,
		"pre_consumed":   preConsumed,
		"token_consumed": tokenConsumed,
		"extra_reserved": extraReserved,
	})
	s.mu.Unlock()

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		userId,
		logger.FormatQuota(tokenConsumed),
		fundingSource,
	))

	gopool.Go(func() {
		fundingRefunded := true
		extraReservedRefunded := true
		tokenRefunded := true

		// 1) 退还资金来源
		if err := funding.Refund(); err != nil {
			fundingRefunded = false
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":          "refund_funding_error",
				"request_id":     requestId,
				"user_id":        userId,
				"funding_source": fundingSource,
				"error":          err.Error(),
			})
			common.SysLog("error refunding billing source: " + err.Error())
		} else {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":          "refund_funding_done",
				"request_id":     requestId,
				"user_id":        userId,
				"funding_source": fundingSource,
			})
		}

		if extraReserved > 0 && fundingSource == BillingSourceSubscription && subscriptionId > 0 {
			if err := model.PostConsumeUserSubscriptionDelta(subscriptionId, -int64(extraReserved)); err != nil {
				extraReservedRefunded = false
				logger.BillingDebugMap(nil, map[string]interface{}{
					"stage":           "refund_extra_reserved_error",
					"request_id":      requestId,
					"user_id":         userId,
					"subscription_id": subscriptionId,
					"extra_reserved":  extraReserved,
					"error":           err.Error(),
				})
				common.SysLog("error refunding subscription extra reserved quota: " + err.Error())
			} else {
				logger.BillingDebugMap(nil, map[string]interface{}{
					"stage":           "refund_extra_reserved_done",
					"request_id":      requestId,
					"user_id":         userId,
					"subscription_id": subscriptionId,
					"extra_reserved":  extraReserved,
				})
			}
		}

		// 2) 退还令牌额度
		if tokenConsumed > 0 && !isPlayground {
			if err := model.IncreaseTokenQuota(tokenId, tokenKey, tokenConsumed); err != nil {
				tokenRefunded = false
				logger.BillingDebugMap(nil, map[string]interface{}{
					"stage":          "refund_token_error",
					"request_id":     requestId,
					"user_id":        userId,
					"token_id":       tokenId,
					"token_consumed": tokenConsumed,
					"error":          err.Error(),
				})
				common.SysLog("error refunding token quota: " + err.Error())
			} else {
				logger.BillingDebugMap(nil, map[string]interface{}{
					"stage":          "refund_token_done",
					"request_id":     requestId,
					"user_id":        userId,
					"token_id":       tokenId,
					"token_consumed": tokenConsumed,
				})
			}
		}

		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":                   "refund_done",
			"request_id":              requestId,
			"user_id":                 userId,
			"funding_source":          fundingSource,
			"funding_refunded":        fundingRefunded,
			"extra_reserved_refunded": extraReservedRefunded,
			"token_refunded":          tokenRefunded,
		})
	})
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":          "reserve_start",
		"request_id":     s.relayInfo.RequestId,
		"user_id":        s.relayInfo.UserId,
		"funding_source": s.funding.Source(),
		"target_quota":   targetQuota,
		"pre_consumed":   s.preConsumedQuota,
	})

	if s.settled || s.refunded || s.trusted || targetQuota <= s.preConsumedQuota {
		reason := "target_not_greater_than_pre_consumed"
		switch {
		case s.settled:
			reason = "already_settled"
		case s.refunded:
			reason = "already_refunded"
		case s.trusted:
			reason = "trusted_request"
		}
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":          "reserve_skipped",
			"request_id":     s.relayInfo.RequestId,
			"user_id":        s.relayInfo.UserId,
			"funding_source": s.funding.Source(),
			"target_quota":   targetQuota,
			"pre_consumed":   s.preConsumedQuota,
			"reason":         reason,
		})
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":      "reserve_skipped",
			"request_id": s.relayInfo.RequestId,
			"user_id":    s.relayInfo.UserId,
			"reason":     "non_positive_delta",
			"delta":      delta,
		})
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":          "reserve_funding_error",
			"request_id":     s.relayInfo.RequestId,
			"user_id":        s.relayInfo.UserId,
			"funding_source": s.funding.Source(),
			"delta":          delta,
			"error":          err.Error(),
		})
		return err
	}
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":          "reserve_funding_done",
		"request_id":     s.relayInfo.RequestId,
		"user_id":        s.relayInfo.UserId,
		"funding_source": s.funding.Source(),
		"delta":          delta,
	})

	if err := s.reserveToken(delta); err != nil {
		logger.BillingDebugMap(nil, map[string]interface{}{
			"stage":      "reserve_token_error",
			"request_id": s.relayInfo.RequestId,
			"user_id":    s.relayInfo.UserId,
			"token_id":   s.relayInfo.TokenId,
			"delta":      delta,
			"error":      err.Error(),
		})
		s.rollbackFundingReserve(delta)
		return err
	}
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":      "reserve_token_done",
		"request_id": s.relayInfo.RequestId,
		"user_id":    s.relayInfo.UserId,
		"token_id":   s.relayInfo.TokenId,
		"delta":      delta,
		"playground": s.relayInfo.IsPlayground,
	})

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":          "reserve_done",
		"request_id":     s.relayInfo.RequestId,
		"user_id":        s.relayInfo.UserId,
		"funding_source": s.funding.Source(),
		"target_quota":   targetQuota,
		"delta":          delta,
		"pre_consumed":   s.preConsumedQuota,
		"token_consumed": s.tokenConsumed,
		"extra_reserved": s.extraReserved,
	})
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota
	logger.BillingDebugMap(c, map[string]interface{}{
		"stage":           "pre_consume_start",
		"request_id":      s.relayInfo.RequestId,
		"user_id":         s.relayInfo.UserId,
		"funding_source":  s.funding.Source(),
		"requested_quota": quota,
		"token_id":        s.relayInfo.TokenId,
		"playground":      s.relayInfo.IsPlayground,
	})

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":           "pre_consume_trusted",
			"request_id":      s.relayInfo.RequestId,
			"user_id":         s.relayInfo.UserId,
			"funding_source":  s.funding.Source(),
			"requested_quota": quota,
		})
		logger.LogInfo(c, fmt.Sprintf("用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// ---- 1) 预扣令牌额度 ----
	if effectiveQuota > 0 {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":           "pre_consume_token_error",
				"request_id":      s.relayInfo.RequestId,
				"user_id":         s.relayInfo.UserId,
				"token_id":        s.relayInfo.TokenId,
				"effective_quota": effectiveQuota,
				"error_code":      types.ErrorCodePreConsumeTokenQuotaFailed,
				"error":           err.Error(),
			})
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":           "pre_consume_token_done",
			"request_id":      s.relayInfo.RequestId,
			"user_id":         s.relayInfo.UserId,
			"token_id":        s.relayInfo.TokenId,
			"effective_quota": effectiveQuota,
		})
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":           "pre_consume_funding_error",
			"request_id":      s.relayInfo.RequestId,
			"user_id":         s.relayInfo.UserId,
			"funding_source":  s.funding.Source(),
			"effective_quota": effectiveQuota,
			"error":           err.Error(),
		})
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				logger.BillingDebugMap(c, map[string]interface{}{
					"stage":          "pre_consume_token_rollback_error",
					"request_id":     s.relayInfo.RequestId,
					"user_id":        s.relayInfo.UserId,
					"token_id":       s.relayInfo.TokenId,
					"token_consumed": s.tokenConsumed,
					"error":          rollbackErr.Error(),
				})
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			} else {
				logger.BillingDebugMap(c, map[string]interface{}{
					"stage":          "pre_consume_token_rollback_done",
					"request_id":     s.relayInfo.RequestId,
					"user_id":        s.relayInfo.UserId,
					"token_id":       s.relayInfo.TokenId,
					"token_consumed": s.tokenConsumed,
				})
			}
			s.tokenConsumed = 0
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		if errors.Is(err, ErrInsufficientWalletQuota) {
			userQuota, quotaErr := model.GetUserQuota(s.relayInfo.UserId, false)
			if quotaErr != nil {
				userQuota = 0
			}
			return types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %s", errMsg), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	logger.BillingDebugMap(c, map[string]interface{}{
		"stage":           "pre_consume_funding_done",
		"request_id":      s.relayInfo.RequestId,
		"user_id":         s.relayInfo.UserId,
		"funding_source":  s.funding.Source(),
		"effective_quota": effectiveQuota,
	})

	s.preConsumedQuota = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	logger.BillingDebugMap(c, map[string]interface{}{
		"stage":           "pre_consume_executed",
		"request_id":      s.relayInfo.RequestId,
		"user_id":         s.relayInfo.UserId,
		"requested_quota": quota,
		"effective_quota": effectiveQuota,
		"funding_source":  s.funding.Source(),
		"token_consumed":  s.tokenConsumed,
		"trusted":         s.trusted,
	})

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		// 与结算补扣（SettleBilling 正差额 → WalletFunding.Settle）语义一致：
		// 全额无条件扣减，余额不足的部分记为欠费（余额可为负），不中断请求，
		// 保证日志记录的预扣额度与用户余额的实际变动始终对账一致。
		// DecreaseUserQuota 仅在数据库错误时失败。
		if err := model.DecreaseUserQuota(funding.userId, delta, false); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	logger.BillingDebugMap(nil, map[string]interface{}{
		"stage":          "reserve_funding_rollback",
		"request_id":     s.relayInfo.RequestId,
		"user_id":        s.relayInfo.UserId,
		"funding_source": s.funding.Source(),
		"delta":          delta,
	})

	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.IncreaseUserQuota(funding.userId, delta, false); err != nil {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":          "reserve_funding_rollback_error",
				"request_id":     s.relayInfo.RequestId,
				"user_id":        s.relayInfo.UserId,
				"funding_source": s.funding.Source(),
				"delta":          delta,
				"error":          err.Error(),
			})
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		} else {
			funding.consumed -= delta
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":          "reserve_funding_rollback_done",
				"request_id":     s.relayInfo.RequestId,
				"user_id":        s.relayInfo.UserId,
				"funding_source": s.funding.Source(),
				"delta":          delta,
			})
		}
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":           "reserve_funding_rollback_error",
				"request_id":      s.relayInfo.RequestId,
				"user_id":         s.relayInfo.UserId,
				"funding_source":  s.funding.Source(),
				"subscription_id": funding.subscriptionId,
				"delta":           delta,
				"error":           err.Error(),
			})
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		} else {
			logger.BillingDebugMap(nil, map[string]interface{}{
				"stage":           "reserve_funding_rollback_done",
				"request_id":      s.relayInfo.RequestId,
				"user_id":         s.relayInfo.UserId,
				"funding_source":  s.funding.Source(),
				"subscription_id": funding.subscriptionId,
				"delta":           delta,
			})
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = tokenQuota > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return s.relayInfo.UserQuota > trustQuota
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":  "session_create_error",
			"reason": "nil_relay_info",
			"error":  "relayInfo is nil",
		})
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)
	logger.BillingDebugMap(c, map[string]interface{}{
		"stage":              "session_create",
		"request_id":         relayInfo.RequestId,
		"user_id":            relayInfo.UserId,
		"pre_consumed_quota": preConsumedQuota,
		"billing_preference": pref,
		"model":              relayInfo.OriginModelName,
	})

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":          "session_wallet_try",
			"request_id":     relayInfo.RequestId,
			"user_id":        relayInfo.UserId,
			"required_quota": preConsumedQuota,
		})
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_wallet_query_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"error":      err.Error(),
			})
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":           "session_wallet_insufficient",
				"request_id":      relayInfo.RequestId,
				"user_id":         relayInfo.UserId,
				"available_quota": userQuota,
				"required_quota":  preConsumedQuota,
				"reason":          "no_wallet_quota",
			})
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota-preConsumedQuota < 0 {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":           "session_wallet_insufficient",
				"request_id":      relayInfo.RequestId,
				"user_id":         relayInfo.UserId,
				"available_quota": userQuota,
				"required_quota":  preConsumedQuota,
				"reason":          "pre_consume_exceeds_wallet",
			})
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WalletFunding{userId: relayInfo.UserId},
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_wallet_pre_consume_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"error_code": apiErr.GetErrorCode(),
				"error":      apiErr.Error(),
			})
			return nil, apiErr
		}
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":          "session_wallet_ready",
			"request_id":     relayInfo.RequestId,
			"user_id":        relayInfo.UserId,
			"funding_source": BillingSourceWallet,
			"pre_consumed":   session.preConsumedQuota,
			"trusted":        session.trusted,
		})
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":          "session_subscription_try",
			"request_id":     relayInfo.RequestId,
			"user_id":        relayInfo.UserId,
			"required_quota": subConsume,
		})
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.OriginModelName,
				amount:    subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_subscription_pre_consume_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"error_code": apiErr.GetErrorCode(),
				"error":      apiErr.Error(),
			})
			return nil, apiErr
		}
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":           "session_subscription_ready",
			"request_id":      relayInfo.RequestId,
			"user_id":         relayInfo.UserId,
			"funding_source":  BillingSourceSubscription,
			"subscription_id": session.relayInfo.SubscriptionId,
			"pre_consumed":    session.preConsumedQuota,
		})
		return session, nil
	}

	switch pref {
	case "subscription_only":
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":      "session_preference_selected",
			"request_id": relayInfo.RequestId,
			"user_id":    relayInfo.UserId,
			"preference": pref,
		})
		return trySubscription()
	case "wallet_only":
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":      "session_preference_selected",
			"request_id": relayInfo.RequestId,
			"user_id":    relayInfo.UserId,
			"preference": pref,
		})
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				logger.BillingDebugMap(c, map[string]interface{}{
					"stage":      "session_fallback",
					"request_id": relayInfo.RequestId,
					"user_id":    relayInfo.UserId,
					"from":       BillingSourceWallet,
					"to":         BillingSourceSubscription,
					"reason":     "wallet_insufficient",
					"error_code": err.GetErrorCode(),
				})
				return trySubscription()
			}
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_create_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"source":     BillingSourceWallet,
				"error_code": err.GetErrorCode(),
				"error":      err.Error(),
			})
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_subscription_check_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"error":      subCheckErr.Error(),
			})
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		logger.BillingDebugMap(c, map[string]interface{}{
			"stage":      "session_subscription_check",
			"request_id": relayInfo.RequestId,
			"user_id":    relayInfo.UserId,
			"has_active": hasSub,
			"preference": pref,
		})
		if !hasSub {
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_fallback",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"from":       BillingSourceSubscription,
				"to":         BillingSourceWallet,
				"reason":     "no_active_subscription",
			})
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if overflowErr != nil {
					logger.BillingDebugMap(c, map[string]interface{}{
						"stage":      "session_overflow_check_error",
						"request_id": relayInfo.RequestId,
						"user_id":    relayInfo.UserId,
						"error":      overflowErr.Error(),
					})
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					logger.BillingDebugMap(c, map[string]interface{}{
						"stage":      "session_fallback",
						"request_id": relayInfo.RequestId,
						"user_id":    relayInfo.UserId,
						"from":       BillingSourceSubscription,
						"to":         BillingSourceWallet,
						"reason":     "subscription_insufficient_overflow_allowed",
						"error_code": apiErr.GetErrorCode(),
					})
					return tryWallet()
				}
				logger.BillingDebugMap(c, map[string]interface{}{
					"stage":      "session_fallback_skipped",
					"request_id": relayInfo.RequestId,
					"user_id":    relayInfo.UserId,
					"from":       BillingSourceSubscription,
					"to":         BillingSourceWallet,
					"reason":     "subscription_insufficient_overflow_denied",
					"error_code": apiErr.GetErrorCode(),
				})
				return nil, apiErr
			}
			logger.BillingDebugMap(c, map[string]interface{}{
				"stage":      "session_create_error",
				"request_id": relayInfo.RequestId,
				"user_id":    relayInfo.UserId,
				"source":     BillingSourceSubscription,
				"error_code": apiErr.GetErrorCode(),
				"error":      apiErr.Error(),
			})
			return nil, apiErr
		}
		return session, nil
	}
}
