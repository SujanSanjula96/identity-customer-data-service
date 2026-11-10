/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package httpmw

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

// CorrelationIDHeader is the header that carries the correlation ID in the request and in the
// response.
const CorrelationIDHeader = "X-Correlation-ID"

// requestIDHeader is an alternative request header for the correlation ID.
const requestIDHeader = "X-Request-ID"

const maxUserAgentLength = 256

// correlationIDPattern limits a correlation ID from a client to characters that are safe in a log
// record.
var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestContext puts the reqctx.Info of the request on the context. It takes the correlation ID
// from the request when the client sent a valid one, and creates one otherwise. It returns the
// correlation ID in the response.
func RequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get(CorrelationIDHeader)
		if correlationID == "" {
			correlationID = r.Header.Get(requestIDHeader)
		}
		if !correlationIDPattern.MatchString(correlationID) {
			correlationID = uuid.NewString()
		}
		w.Header().Set(CorrelationIDHeader, correlationID)

		info := reqctx.New(correlationID, clientIP(r), truncate(r.UserAgent(), maxUserAgentLength))
		next.ServeHTTP(w, r.WithContext(reqctx.With(r.Context(), info)))
	})
}

// AccessLog writes one record for each request to logger. It does not log a health or readiness
// request that succeeded. It never logs the query string, because a query can carry personal data.
func AccessLog(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := r.URL.Path
		rec := NewRecorder(w)

		next.ServeHTTP(rec, r)

		if rec.Status() < http.StatusBadRequest && isProbe(path) {
			return
		}
		ip, userAgent := reqctx.From(r.Context()).Client()
		fields := []log.Field{
			log.String("method", r.Method),
			log.String("path", path),
			log.String("protocol", r.Proto),
			log.Int("status", rec.Status()),
			log.Int("bytes", rec.Size()),
			log.Any("duration_ms", time.Since(start).Milliseconds()),
			log.String("client_ip", ip),
			log.String("user_agent", userAgent),
		}
		if code := rec.ErrorCode(); code != "" {
			fields = append(fields, log.String("error_code", code))
		}
		logger.WithContext(r.Context()).Info("request", fields...)
	})
}

// isProbe reports whether path is a health or readiness endpoint.
func isProbe(path string) bool {
	return strings.HasSuffix(path, "/v1/health") || strings.HasSuffix(path, "/v1/ready")
}

// clientIP returns the address of the TCP peer. It does not use the forwarded headers, because a
// client can set them.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
