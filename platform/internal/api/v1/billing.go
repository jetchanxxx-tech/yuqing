package v1

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/payment"
)

// RegisterBillingRoutes mounts the billing endpoints（方案 B 收费体系）:
// 套餐目录 / 额度池 / 订单（下单、轮询含查单对账）/ 银联收银台跳转页。
func RegisterBillingRoutes(r *gin.RouterGroup, svcs *Services) {
	billingGroup := r.Group("/billing")
	billingGroup.GET("/plans", svcs.handleListPlans)
	billingGroup.GET("/credits", svcs.handleGetCredits)
	billingGroup.GET("/transactions", svcs.handleListCreditTransactions)
	billingGroup.POST("/orders", svcs.handleCreateOrder)
	billingGroup.GET("/orders", svcs.handleListOrders)
	billingGroup.GET("/orders/:id", svcs.handleGetOrder)
	billingGroup.GET("/orders/:id/paypage", svcs.handleOrderPayPage)
	// 旧占位端点（契约测试锁定其 JSON envelope；订阅/账单仍未实现）
	billingGroup.GET("/subscription", svcs.handleGetSubscription)
	billingGroup.POST("/subscribe", svcs.handleSubscribe)
	billingGroup.GET("/usage", svcs.handleGetUsage)
	billingGroup.GET("/invoices", svcs.handleListInvoices)
	billingGroup.GET("/invoices/:id/download", svcs.handleDownloadInvoice)
}

// ── 旧占位端点（订阅制时代的形状，待 P2 订阅化时替换）────────────

// handleGetSubscription: placeholder until the subscription store exists —
// every tenant renders as the free plan.
func (s *Services) handleGetSubscription(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plan": "free", "status": "active"})
}

// handleSubscribe: placeholder until plan changes are transactional.
func (s *Services) handleSubscribe(c *gin.Context) {
	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "request body must be JSON {plan_code}")
		return
	}
	if req.PlanCode == "" {
		badRequest(c, "plan_code is required")
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": gin.H{"plan": req.PlanCode, "status": "active"}})
}

// handleGetUsage: placeholder until the shared usage meter is wired to the
// tenant's token counter.
func (s *Services) handleGetUsage(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tokens_used":    0,
		"tokens_quota":   1000000,
		"analyses_used":  0,
		"analyses_quota": 5,
	})
}

// handleListInvoices: placeholder until invoice generation (worker) persists.
func (s *Services) handleListInvoices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"invoices": []gin.H{}, "total": 0})
}

// handleDownloadInvoice: invoice file storage is pending — JSON envelope.
func (s *Services) handleDownloadInvoice(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"code":       "NOT_IMPLEMENTED",
		"message":    "invoice download not implemented (invoice file storage pending)",
		"request_id": requestID(c),
	})
}

// handleListPlans 套餐目录 + 加购 SKU + 当前可用支付渠道。
func (s *Services) handleListPlans(c *gin.Context) {
	plans := sortedPlans()

	addons := make([]*billing.SKU, 0, len(billing.AddonSkus()))
	for _, sku := range billing.AddonSkus() {
		addons = append(addons, sku)
	}

	channels := availableChannels(c, s)
	c.JSON(http.StatusOK, gin.H{
		"plans":    plans,
		"addons":   addons,
		"channels": channels,
	})
}

// availableChannels 返回当前配置且启用的支付渠道（未配置渠道不展示给用户，
// 也无法下单 —— 双重 fail-closed）。
func availableChannels(c *gin.Context, s *Services) []string {
	if s.PaymentRegistry == nil {
		return []string{}
	}
	providers, err := s.PaymentRegistry.Resolve(c.Request.Context())
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(providers))
	for _, ch := range []string{payment.ChannelAlipay, payment.ChannelWechat, payment.ChannelUnionPay} {
		if p, ok := providers[ch]; ok && p.Configured() {
			out = append(out, ch)
		}
	}
	return out
}

// handleGetCredits 额度余额 + 当前套餐标记。
func (s *Services) handleGetCredits(c *gin.Context) {
	tenantID, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	balance, err := s.Credits.Balance(c.Request.Context(), tenantID)
	if err != nil {
		respondError(c, err)
		return
	}
	planCode, _ := s.Credits.PlanCode(c.Request.Context(), tenantID)
	c.JSON(http.StatusOK, gin.H{
		"balance":   balance,
		"plan_code": planCode,
	})
}

// handleListCreditTransactions 额度流水（消费/入账/回补）。
func (s *Services) handleListCreditTransactions(c *gin.Context) {
	tenantID, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	txs, err := s.Credits.Transactions(c.Request.Context(), tenantID, 100)
	if err != nil {
		respondError(c, err)
		return
	}
	if txs == nil {
		txs = []credit.Transaction{}
	}
	c.JSON(http.StatusOK, gin.H{"transactions": txs, "total": len(txs)})
}

// handleCreateOrder 下单：{sku_code, channel} → pending 订单 + 二维码内容。
// 金额与额度取自服务端目录，前端传入的只有商品选择与渠道。
func (s *Services) handleCreateOrder(c *gin.Context) {
	tenantID, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	var req struct {
		SKUCode string `json:"sku_code"`
		Channel string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SKUCode == "" {
		badRequest(c, "body must be JSON {sku_code, channel}")
		return
	}
	order, err := s.Payment.Create(c.Request.Context(), tenantID, req.SKUCode, req.Channel)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"order": order})
}

// handleListOrders 租户订单历史。
func (s *Services) handleListOrders(c *gin.Context) {
	tenantID, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	orders, err := s.Payment.List(c.Request.Context(), tenantID, 50)
	if err != nil {
		respondError(c, err)
		return
	}
	if orders == nil {
		orders = []*payment.Order{}
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders, "total": len(orders)})
}

// handleGetOrder 订单详情。轮询即对账：pending 订单顺带向渠道主动查单，
// 回调丢失（付款了没到账）在这里自愈入账。
func (s *Services) handleGetOrder(c *gin.Context) {
	tenantID, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	order, err := s.Payment.Reconcile(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, payment.ErrNotFound) {
			notFound(c, "order not found")
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order})
}

// handleOrderPayPage 银联收银台跳转页（GET + query token：浏览器/新窗口
// 无法携带 Authorization 头）。校验 JWT 后输出自动提交的表单 HTML。
func (s *Services) handleOrderPayPage(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.String(http.StatusUnauthorized, "missing token")
		return
	}
	principal, err := s.Auth.Authenticate(c.Request.Context(), token)
	if err != nil {
		c.String(http.StatusUnauthorized, "invalid token")
		return
	}
	order, err := s.Payment.Reconcile(c.Request.Context(), principal.TenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, payment.ErrNotFound) {
			c.String(http.StatusNotFound, "order not found")
			return
		}
		c.String(http.StatusInternalServerError, "reconcile failed")
		return
	}
	if order.Channel != payment.ChannelUnionPay || !strings.Contains(order.QRCodeURL, "<form") {
		c.String(http.StatusBadRequest, "order has no unionpay checkout form")
		return
	}
	if order.State != payment.StatePending {
		c.String(http.StatusConflict, "order is no longer pending")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(order.QRCodeURL))
}
