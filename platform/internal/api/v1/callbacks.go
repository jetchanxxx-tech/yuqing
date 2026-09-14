package v1

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/yuqing/platform/internal/platform/payment"
)

// HandlePaymentCallback 渠道回调入口（公开路由，安全由验签保证 ——
// 这是支付防线 1：任何未经渠道签名的回调在此被拒绝）。
//
// 响应语义按渠道：支付宝 "success" 纯文本 / 微信 JSON / 银联 form。
// 验签或金额核验失败返回 400（渠道会按自身策略重试，运营可从日志追查）。
func HandlePaymentCallback(svcs *Services) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := c.Param("channel")

		if err := c.Request.ParseForm(); err != nil {
			c.String(http.StatusBadRequest, "bad form")
			return
		}
		form := map[string]string{}
		for k, v := range c.Request.PostForm {
			if len(v) > 0 {
				form[k] = v[0]
			}
		}
		query := map[string]string{}
		for k, v := range c.Request.URL.Query() {
			if len(v) > 0 {
				query[k] = v[0]
			}
		}
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		header := map[string]string{}
		for k := range c.Request.Header {
			header[k] = c.Request.Header.Get(k)
		}

		status, contentType, respBody, err := svcs.Payment.HandleCallback(c.Request.Context(), channel, &payment.CallbackInput{
			Body:   body,
			Form:   form,
			Query:  query,
			Header: header,
		})
		if err != nil {
			c.String(http.StatusBadRequest, "rejected")
			return
		}
		c.Data(status, contentType, []byte(respBody))
	}
}
