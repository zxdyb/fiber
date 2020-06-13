// ⚡️ Fiber is an Express inspired web framework written in Go with ☕️
// 🤖 Github Repository: https://github.com/gofiber/fiber
// 📌 API Documentation: https://docs.gofiber.io

package fiber

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"net"
	"strings"
	"sync/atomic"
	"time"

	utils "github.com/gofiber/utils"
)

// Generate and set ETag header to response
func setETag(ctx *Ctx, weak bool) {
	// Don't generate ETags for invalid responses
	if ctx.Fasthttp.Response.StatusCode() != 200 {
		return
	}
	body := ctx.Fasthttp.Response.Body()
	// Skips ETag if no response body is present
	if len(body) <= 0 {
		return
	}
	// Get ETag header from request
	clientEtag := ctx.Get(HeaderIfNoneMatch)

	// Generate ETag for response
	crc32q := crc32.MakeTable(0xD5828281)
	etag := fmt.Sprintf("\"%d-%v\"", len(body), crc32.Checksum(body, crc32q))

	// Enable weak tag
	if weak {
		etag = "W/" + etag
	}

	// Check if client's ETag is weak
	if strings.HasPrefix(clientEtag, "W/") {
		// Check if server's ETag is weak
		if clientEtag[2:] == etag || clientEtag[2:] == etag[2:] {
			// W/1 == 1 || W/1 == W/1
			ctx.SendStatus(304)
			ctx.Fasthttp.ResetBody()
			return
		}
		// W/1 != W/2 || W/1 != 2
		ctx.Set(HeaderETag, etag)
		return
	}
	if strings.Contains(clientEtag, etag) {
		// 1 == 1
		ctx.SendStatus(304)
		ctx.Fasthttp.ResetBody()
		return
	}
	// 1 != 2
	ctx.Set(HeaderETag, etag)
}

func getGroupPath(prefix, path string) string {
	if path == "/" {
		return prefix
	}
	return utils.TrimRight(prefix, '/') + path
}

// return valid offer for header negotiation
func getOffer(header string, offers ...string) string {
	if len(offers) == 0 {
		return ""
	} else if header == "" {
		return offers[0]
	}

	spec, commaPos := "", 0
	for len(header) > 0 && commaPos != -1 {
		commaPos = strings.IndexByte(header, ',')
		if commaPos != -1 {
			spec = utils.Trim(header[:commaPos], ' ')
		} else {
			spec = header
		}
		if factorSign := strings.IndexByte(spec, ';'); factorSign != -1 {
			spec = spec[:factorSign]
		}

		for _, offer := range offers {
			// has star prefix
			if len(spec) >= 1 && spec[len(spec)-1] == '*' {
				return offer
			} else if strings.HasPrefix(spec, offer) {
				return offer
			}
		}
		if commaPos != -1 {
			header = header[commaPos+1:]
		}
	}

	return ""
}

// Adapted from:
// https://github.com/jshttp/fresh/blob/10e0471669dbbfbfd8de65bc6efac2ddd0bfa057/index.js#L110
func parseTokenList(noneMatchBytes []byte) []string {
	var (
		start int
		end   int
		list  []string
	)
	for i := range noneMatchBytes {
		switch noneMatchBytes[i] {
		case 0x20:
			if start == end {
				start = i + 1
				end = i + 1
			}
		case 0x2c:
			list = append(list, getString(noneMatchBytes[start:end]))
			start = i + 1
			end = i + 1
		default:
			end = i + 1
		}
	}

	list = append(list, getString(noneMatchBytes[start:end]))
	return list
}

// https://golang.org/src/net/net.go#L113
// Helper methods for application#test
type testConn struct {
	net.Conn
	r bytes.Buffer
	w bytes.Buffer
}

func (c *testConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{
		IP: net.IPv4(0, 0, 0, 0),
	}
}
func (c *testConn) LocalAddr() net.Addr                { return c.RemoteAddr() }
func (c *testConn) Read(b []byte) (int, error)         { return c.r.Read(b) }
func (c *testConn) Write(b []byte) (int, error)        { return c.w.Write(b) }
func (c *testConn) Close() error                       { return nil }
func (c *testConn) SetDeadline(t time.Time) error      { return nil }
func (c *testConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *testConn) SetWriteDeadline(t time.Time) error { return nil }

// getString converts byte slice to a string without memory allocation.
var getString = utils.GetString
var getStringImmutable = func(b []byte) string {
	return string(b)
}

// getBytes converts string to a byte slice without memory allocation.
var getBytes = utils.GetBytes
var getBytesImmutable = func(s string) (b []byte) {
	return []byte(s)
}

// ⚠️ This path parser was based on urlpath by @ucarion (MIT License).
// 💖 Modified for the Fiber router by @renanbastos93 & @renewerner87
// 🤖 ucarion/urlpath - renanbastos93/fastpath - renewerner87/fastpath

// routeParser  holds the path segments and param names
type routeParser struct {
	segs   []paramSeg
	params []string
}

// paramsSeg holds the segment metadata
type paramSeg struct {
	Param      string
	Const      string
	IsParam    bool
	IsOptional bool
	IsLast     bool
	EndChar    byte
}

const wildcardParam string = "*"

// New ...
func parseRoute(pattern string) (p routeParser) {
	var out []paramSeg
	var params []string

	part, delimiterPos := "", 0
	for len(pattern) > 0 && delimiterPos != -1 {
		delimiterPos = findNextRouteDelimiterPosition(pattern)
		if delimiterPos != -1 {
			part = pattern[:delimiterPos]
		} else {
			part = pattern
		}

		partLen, lastSeg := len(part), len(out)-1
		if partLen == 0 { // skip empty parts
			if len(pattern) > 0 {
				// remove first char
				pattern = pattern[1:]
			}
			continue
		}
		// is parameter ?
		if part[0] == '*' || part[0] == ':' {
			out = append(out, paramSeg{
				Param:      utils.GetTrimmedParam(part),
				IsParam:    true,
				IsOptional: part == wildcardParam || part[partLen-1] == '?',
			})
			lastSeg = len(out) - 1
			params = append(params, out[lastSeg].Param)
			// combine const segments
		} else if lastSeg >= 0 && !out[lastSeg].IsParam {
			out[lastSeg].Const += string(out[lastSeg].EndChar) + part
			// create new const segment
		} else {
			out = append(out, paramSeg{
				Const: part,
			})
			lastSeg = len(out) - 1
		}

		if delimiterPos != -1 && len(pattern) >= delimiterPos+1 {
			out[lastSeg].EndChar = pattern[delimiterPos]
			pattern = pattern[delimiterPos+1:]
		} else {
			// last default char
			out[lastSeg].EndChar = '/'
		}
	}
	if len(out) > 0 {
		out[len(out)-1].IsLast = true
	}

	p = routeParser{segs: out, params: params}
	return
}

var routeDelimiter = []byte{'/', '-', '.'}

func findNextRouteDelimiterPosition(search string) int {
	nextPosition := -1
	for _, delimiter := range routeDelimiter {
		if pos := strings.IndexByte(search, delimiter); pos != -1 && (pos < nextPosition || nextPosition == -1) {
			nextPosition = pos
		}
	}

	return nextPosition
}

// Match ...
func (p *routeParser) getMatch(s string, partialCheck bool) ([][2]int, bool) {
	lenKeys := len(p.params)
	paramsPositions := getAllocFreeParamsPos(lenKeys)
	var i, j, paramsIterator, partLen, paramStart int
	if len(s) > 0 {
		s = s[1:]
		paramStart++
	}
	for index, segment := range p.segs {
		partLen = len(s)
		// check parameter
		if segment.IsParam {
			// determine parameter length
			if segment.Param == wildcardParam {
				if segment.IsLast {
					i = partLen
				} else {
					i = findWildcardParamLen(s, p.segs, index)
				}
			} else {
				i = strings.IndexByte(s, segment.EndChar)
			}
			if i == -1 {
				i = partLen
			}

			if !segment.IsOptional && i == 0 {
				return nil, false
			}

			paramsPositions[paramsIterator][0], paramsPositions[paramsIterator][1] = paramStart, paramStart+i
			paramsIterator++
		} else {
			// check const segment
			i = len(segment.Const)
			if partLen < i || (i == 0 && partLen > 0) || s[:i] != segment.Const || (partLen > i && s[i] != segment.EndChar) {
				return nil, false
			}
		}

		// reduce founded part from the string
		if partLen > 0 {
			j = i + 1
			if segment.IsLast || partLen < j {
				j = i
			}
			paramStart += j

			s = s[j:]
		}
	}
	if len(s) != 0 && !partialCheck {
		return nil, false
	}

	return paramsPositions, true
}

// get parameters for the given positions from the given path
func (p *routeParser) paramsForPos(path string, paramsPositions [][2]int) []string {
	size := len(paramsPositions)
	params := getAllocFreeParams(size)
	for i, positions := range paramsPositions {
		if positions[0] != positions[1] && len(path) >= positions[1] {
			params[i] = path[positions[0]:positions[1]]
		} else {
			params[i] = ""
		}
	}

	return params
}

// findWildcardParamLen for the expressjs wildcard behavior (right to left greedy)
// look at the other segments and take what is left for the wildcard from right to left
func findWildcardParamLen(s string, segments []paramSeg, currIndex int) int {
	// "/api/*/:param" - "/api/joker/batman/robin/1" -> "joker/batman/robin", "1"
	// "/api/*/:param" - "/api/joker/batman"         -> "joker", "batman"
	// "/api/*/:param" - "/api/joker/batman/robin"   -> "joker/batman", "robin"
	// "/api/*/:param" - "/api/joker-batman-robin/1" -> "joker-batman-robin", "1"
	endChar := segments[currIndex].EndChar
	neededEndChars := 0
	// count the needed chars for the other segments
	for i := currIndex + 1; i < len(segments); i++ {
		if segments[i].EndChar == endChar {
			neededEndChars++
		}
	}
	// remove the part the other segments still need
	for {
		pos := strings.LastIndexByte(s, endChar)
		if pos != -1 {
			s = s[:pos]
		}
		neededEndChars--
		if neededEndChars <= 0 || pos == -1 {
			break
		}
	}

	return len(s)
}

// performance tricks
var paramsDummy = make([]string, 100000)
var paramsPosDummy = make([][2]int, 100000)
var startParamList, startParamPosList uint32 = 0, 0

func getAllocFreeParamsPos(allocLen int) [][2]int {
	size := uint32(allocLen)
	start := atomic.AddUint32(&startParamPosList, size)
	if (start + 10) >= uint32(len(paramsPosDummy)) {
		atomic.StoreUint32(&startParamPosList, 0)
		return getAllocFreeParamsPos(allocLen)
	}
	start -= size
	allocLen += int(start)
	paramsPositions := paramsPosDummy[start:allocLen:allocLen]
	return paramsPositions
}
func getAllocFreeParams(allocLen int) []string {
	size := uint32(allocLen)
	start := atomic.AddUint32(&startParamList, size)
	if (start + 10) >= uint32(len(paramsPosDummy)) {
		atomic.StoreUint32(&startParamList, 0)
		return getAllocFreeParams(allocLen)
	}
	start -= size
	allocLen += int(start)
	params := paramsDummy[start:allocLen:allocLen]
	return params
}

// HTTP methods and their unique INTs
var methodINT = map[string]int{
	MethodGet:     0,
	MethodHead:    1,
	MethodPost:    2,
	MethodPut:     3,
	MethodDelete:  4,
	MethodConnect: 5,
	MethodOptions: 6,
	MethodTrace:   7,
	MethodPatch:   8,
}

// HTTP methods were copied from net/http.
const (
	MethodGet     = "GET"     // RFC 7231, 4.3.1
	MethodHead    = "HEAD"    // RFC 7231, 4.3.2
	MethodPost    = "POST"    // RFC 7231, 4.3.3
	MethodPut     = "PUT"     // RFC 7231, 4.3.4
	MethodPatch   = "PATCH"   // RFC 5789
	MethodDelete  = "DELETE"  // RFC 7231, 4.3.5
	MethodConnect = "CONNECT" // RFC 7231, 4.3.6
	MethodOptions = "OPTIONS" // RFC 7231, 4.3.7
	MethodTrace   = "TRACE"   // RFC 7231, 4.3.8
)

// MIME types that are commonly used
const (
	MIMETextXML               = "text/xml"
	MIMETextHTML              = "text/html"
	MIMETextPlain             = "text/plain"
	MIMEApplicationXML        = "application/xml"
	MIMEApplicationJSON       = "application/json"
	MIMEApplicationJavaScript = "application/javascript"
	MIMEApplicationForm       = "application/x-www-form-urlencoded"
	MIMEOctetStream           = "application/octet-stream"
	MIMEMultipartForm         = "multipart/form-data"

	MIMETextXMLCharsetUTF8               = "text/xml; charset=utf-8"
	MIMETextHTMLCharsetUTF8              = "text/html; charset=utf-8"
	MIMETextPlainCharsetUTF8             = "text/plain; charset=utf-8"
	MIMEApplicationXMLCharsetUTF8        = "application/xml; charset=utf-8"
	MIMEApplicationJavaScriptCharsetUTF8 = "application/javascript; charset=utf-8"
)

// HTTP status codes were copied from net/http.
const (
	StatusContinue                      = 100 // RFC 7231, 6.2.1
	StatusSwitchingProtocols            = 101 // RFC 7231, 6.2.2
	StatusProcessing                    = 102 // RFC 2518, 10.1
	StatusEarlyHints                    = 103 // RFC 8297
	StatusOK                            = 200 // RFC 7231, 6.3.1
	StatusCreated                       = 201 // RFC 7231, 6.3.2
	StatusAccepted                      = 202 // RFC 7231, 6.3.3
	StatusNonAuthoritativeInfo          = 203 // RFC 7231, 6.3.4
	StatusNoContent                     = 204 // RFC 7231, 6.3.5
	StatusResetContent                  = 205 // RFC 7231, 6.3.6
	StatusPartialContent                = 206 // RFC 7233, 4.1
	StatusMultiStatus                   = 207 // RFC 4918, 11.1
	StatusAlreadyReported               = 208 // RFC 5842, 7.1
	StatusIMUsed                        = 226 // RFC 3229, 10.4.1
	StatusMultipleChoices               = 300 // RFC 7231, 6.4.1
	StatusMovedPermanently              = 301 // RFC 7231, 6.4.2
	StatusFound                         = 302 // RFC 7231, 6.4.3
	StatusSeeOther                      = 303 // RFC 7231, 6.4.4
	StatusNotModified                   = 304 // RFC 7232, 4.1
	StatusUseProxy                      = 305 // RFC 7231, 6.4.5
	StatusTemporaryRedirect             = 307 // RFC 7231, 6.4.7
	StatusPermanentRedirect             = 308 // RFC 7538, 3
	StatusBadRequest                    = 400 // RFC 7231, 6.5.1
	StatusUnauthorized                  = 401 // RFC 7235, 3.1
	StatusPaymentRequired               = 402 // RFC 7231, 6.5.2
	StatusForbidden                     = 403 // RFC 7231, 6.5.3
	StatusNotFound                      = 404 // RFC 7231, 6.5.4
	StatusMethodNotAllowed              = 405 // RFC 7231, 6.5.5
	StatusNotAcceptable                 = 406 // RFC 7231, 6.5.6
	StatusProxyAuthRequired             = 407 // RFC 7235, 3.2
	StatusRequestTimeout                = 408 // RFC 7231, 6.5.7
	StatusConflict                      = 409 // RFC 7231, 6.5.8
	StatusGone                          = 410 // RFC 7231, 6.5.9
	StatusLengthRequired                = 411 // RFC 7231, 6.5.10
	StatusPreconditionFailed            = 412 // RFC 7232, 4.2
	StatusRequestEntityTooLarge         = 413 // RFC 7231, 6.5.11
	StatusRequestURITooLong             = 414 // RFC 7231, 6.5.12
	StatusUnsupportedMediaType          = 415 // RFC 7231, 6.5.13
	StatusRequestedRangeNotSatisfiable  = 416 // RFC 7233, 4.4
	StatusExpectationFailed             = 417 // RFC 7231, 6.5.14
	StatusTeapot                        = 418 // RFC 7168, 2.3.3
	StatusMisdirectedRequest            = 421 // RFC 7540, 9.1.2
	StatusUnprocessableEntity           = 422 // RFC 4918, 11.2
	StatusLocked                        = 423 // RFC 4918, 11.3
	StatusFailedDependency              = 424 // RFC 4918, 11.4
	StatusTooEarly                      = 425 // RFC 8470, 5.2.
	StatusUpgradeRequired               = 426 // RFC 7231, 6.5.15
	StatusPreconditionRequired          = 428 // RFC 6585, 3
	StatusTooManyRequests               = 429 // RFC 6585, 4
	StatusRequestHeaderFieldsTooLarge   = 431 // RFC 6585, 5
	StatusUnavailableForLegalReasons    = 451 // RFC 7725, 3
	StatusInternalServerError           = 500 // RFC 7231, 6.6.1
	StatusNotImplemented                = 501 // RFC 7231, 6.6.2
	StatusBadGateway                    = 502 // RFC 7231, 6.6.3
	StatusServiceUnavailable            = 503 // RFC 7231, 6.6.4
	StatusGatewayTimeout                = 504 // RFC 7231, 6.6.5
	StatusHTTPVersionNotSupported       = 505 // RFC 7231, 6.6.6
	StatusVariantAlsoNegotiates         = 506 // RFC 2295, 8.1
	StatusInsufficientStorage           = 507 // RFC 4918, 11.5
	StatusLoopDetected                  = 508 // RFC 5842, 7.2
	StatusNotExtended                   = 510 // RFC 2774, 7
	StatusNetworkAuthenticationRequired = 511 // RFC 6585, 6
)

// Errors
var (
	ErrContinue                      = NewError(StatusContinue)                      // RFC 7231, 6.2.1
	ErrSwitchingProtocols            = NewError(StatusSwitchingProtocols)            // RFC 7231, 6.2.2
	ErrProcessing                    = NewError(StatusProcessing)                    // RFC 2518, 10.1
	ErrEarlyHints                    = NewError(StatusEarlyHints)                    // RFC 8297
	ErrOK                            = NewError(StatusOK)                            // RFC 7231, 6.3.1
	ErrCreated                       = NewError(StatusCreated)                       // RFC 7231, 6.3.2
	ErrAccepted                      = NewError(StatusAccepted)                      // RFC 7231, 6.3.3
	ErrNonAuthoritativeInfo          = NewError(StatusNonAuthoritativeInfo)          // RFC 7231, 6.3.4
	ErrNoContent                     = NewError(StatusNoContent)                     // RFC 7231, 6.3.5
	ErrResetContent                  = NewError(StatusResetContent)                  // RFC 7231, 6.3.6
	ErrPartialContent                = NewError(StatusPartialContent)                // RFC 7233, 4.1
	ErrMultiStatus                   = NewError(StatusMultiStatus)                   // RFC 4918, 11.1
	ErrAlreadyReported               = NewError(StatusAlreadyReported)               // RFC 5842, 7.1
	ErrIMUsed                        = NewError(StatusIMUsed)                        // RFC 3229, 10.4.1
	ErrMultipleChoices               = NewError(StatusMultipleChoices)               // RFC 7231, 6.4.1
	ErrMovedPermanently              = NewError(StatusMovedPermanently)              // RFC 7231, 6.4.2
	ErrFound                         = NewError(StatusFound)                         // RFC 7231, 6.4.3
	ErrSeeOther                      = NewError(StatusSeeOther)                      // RFC 7231, 6.4.4
	ErrNotModified                   = NewError(StatusNotModified)                   // RFC 7232, 4.1
	ErrUseProxy                      = NewError(StatusUseProxy)                      // RFC 7231, 6.4.5
	ErrTemporaryRedirect             = NewError(StatusTemporaryRedirect)             // RFC 7231, 6.4.7
	ErrPermanentRedirect             = NewError(StatusPermanentRedirect)             // RFC 7538, 3
	ErrBadRequest                    = NewError(StatusBadRequest)                    // RFC 7231, 6.5.1
	ErrUnauthorized                  = NewError(StatusUnauthorized)                  // RFC 7235, 3.1
	ErrPaymentRequired               = NewError(StatusPaymentRequired)               // RFC 7231, 6.5.2
	ErrForbidden                     = NewError(StatusForbidden)                     // RFC 7231, 6.5.3
	ErrNotFound                      = NewError(StatusNotFound)                      // RFC 7231, 6.5.4
	ErrMethodNotAllowed              = NewError(StatusMethodNotAllowed)              // RFC 7231, 6.5.5
	ErrNotAcceptable                 = NewError(StatusNotAcceptable)                 // RFC 7231, 6.5.6
	ErrProxyAuthRequired             = NewError(StatusProxyAuthRequired)             // RFC 7235, 3.2
	ErrRequestTimeout                = NewError(StatusRequestTimeout)                // RFC 7231, 6.5.7
	ErrConflict                      = NewError(StatusConflict)                      // RFC 7231, 6.5.8
	ErrGone                          = NewError(StatusGone)                          // RFC 7231, 6.5.9
	ErrLengthRequired                = NewError(StatusLengthRequired)                // RFC 7231, 6.5.10
	ErrPreconditionFailed            = NewError(StatusPreconditionFailed)            // RFC 7232, 4.2
	ErrRequestEntityTooLarge         = NewError(StatusRequestEntityTooLarge)         // RFC 7231, 6.5.11
	ErrRequestURITooLong             = NewError(StatusRequestURITooLong)             // RFC 7231, 6.5.12
	ErrUnsupportedMediaType          = NewError(StatusUnsupportedMediaType)          // RFC 7231, 6.5.13
	ErrRequestedRangeNotSatisfiable  = NewError(StatusRequestedRangeNotSatisfiable)  // RFC 7233, 4.4
	ErrExpectationFailed             = NewError(StatusExpectationFailed)             // RFC 7231, 6.5.14
	ErrTeapot                        = NewError(StatusTeapot)                        // RFC 7168, 2.3.3
	ErrMisdirectedRequest            = NewError(StatusMisdirectedRequest)            // RFC 7540, 9.1.2
	ErrUnprocessableEntity           = NewError(StatusUnprocessableEntity)           // RFC 4918, 11.2
	ErrLocked                        = NewError(StatusLocked)                        // RFC 4918, 11.3
	ErrFailedDependency              = NewError(StatusFailedDependency)              // RFC 4918, 11.4
	ErrTooEarly                      = NewError(StatusTooEarly)                      // RFC 8470, 5.2.
	ErrUpgradeRequired               = NewError(StatusUpgradeRequired)               // RFC 7231, 6.5.15
	ErrPreconditionRequired          = NewError(StatusPreconditionRequired)          // RFC 6585, 3
	ErrTooManyRequests               = NewError(StatusTooManyRequests)               // RFC 6585, 4
	ErrRequestHeaderFieldsTooLarge   = NewError(StatusRequestHeaderFieldsTooLarge)   // RFC 6585, 5
	ErrUnavailableForLegalReasons    = NewError(StatusUnavailableForLegalReasons)    // RFC 7725, 3
	ErrInternalServerError           = NewError(StatusInternalServerError)           // RFC 7231, 6.6.1
	ErrNotImplemented                = NewError(StatusNotImplemented)                // RFC 7231, 6.6.2
	ErrBadGateway                    = NewError(StatusBadGateway)                    // RFC 7231, 6.6.3
	ErrServiceUnavailable            = NewError(StatusServiceUnavailable)            // RFC 7231, 6.6.4
	ErrGatewayTimeout                = NewError(StatusGatewayTimeout)                // RFC 7231, 6.6.5
	ErrHTTPVersionNotSupported       = NewError(StatusHTTPVersionNotSupported)       // RFC 7231, 6.6.6
	ErrVariantAlsoNegotiates         = NewError(StatusVariantAlsoNegotiates)         // RFC 2295, 8.1
	ErrInsufficientStorage           = NewError(StatusInsufficientStorage)           // RFC 4918, 11.5
	ErrLoopDetected                  = NewError(StatusLoopDetected)                  // RFC 5842, 7.2
	ErrNotExtended                   = NewError(StatusNotExtended)                   // RFC 2774, 7
	ErrNetworkAuthenticationRequired = NewError(StatusNetworkAuthenticationRequired) // RFC 6585, 6
)

// HTTP Headers were copied from net/http.
const (
	HeaderAuthorization                   = "Authorization"
	HeaderProxyAuthenticate               = "Proxy-Authenticate"
	HeaderProxyAuthorization              = "Proxy-Authorization"
	HeaderWWWAuthenticate                 = "WWW-Authenticate"
	HeaderAge                             = "Age"
	HeaderCacheControl                    = "Cache-Control"
	HeaderClearSiteData                   = "Clear-Site-Data"
	HeaderExpires                         = "Expires"
	HeaderPragma                          = "Pragma"
	HeaderWarning                         = "Warning"
	HeaderAcceptCH                        = "Accept-CH"
	HeaderAcceptCHLifetime                = "Accept-CH-Lifetime"
	HeaderContentDPR                      = "Content-DPR"
	HeaderDPR                             = "DPR"
	HeaderEarlyData                       = "Early-Data"
	HeaderSaveData                        = "Save-Data"
	HeaderViewportWidth                   = "Viewport-Width"
	HeaderWidth                           = "Width"
	HeaderETag                            = "ETag"
	HeaderIfMatch                         = "If-Match"
	HeaderIfModifiedSince                 = "If-Modified-Since"
	HeaderIfNoneMatch                     = "If-None-Match"
	HeaderIfUnmodifiedSince               = "If-Unmodified-Since"
	HeaderLastModified                    = "Last-Modified"
	HeaderVary                            = "Vary"
	HeaderConnection                      = "Connection"
	HeaderKeepAlive                       = "Keep-Alive"
	HeaderAccept                          = "Accept"
	HeaderAcceptCharset                   = "Accept-Charset"
	HeaderAcceptEncoding                  = "Accept-Encoding"
	HeaderAcceptLanguage                  = "Accept-Language"
	HeaderCookie                          = "Cookie"
	HeaderExpect                          = "Expect"
	HeaderMaxForwards                     = "Max-Forwards"
	HeaderSetCookie                       = "Set-Cookie"
	HeaderAccessControlAllowCredentials   = "Access-Control-Allow-Credentials"
	HeaderAccessControlAllowHeaders       = "Access-Control-Allow-Headers"
	HeaderAccessControlAllowMethods       = "Access-Control-Allow-Methods"
	HeaderAccessControlAllowOrigin        = "Access-Control-Allow-Origin"
	HeaderAccessControlExposeHeaders      = "Access-Control-Expose-Headers"
	HeaderAccessControlMaxAge             = "Access-Control-Max-Age"
	HeaderAccessControlRequestHeaders     = "Access-Control-Request-Headers"
	HeaderAccessControlRequestMethod      = "Access-Control-Request-Method"
	HeaderOrigin                          = "Origin"
	HeaderTimingAllowOrigin               = "Timing-Allow-Origin"
	HeaderXPermittedCrossDomainPolicies   = "X-Permitted-Cross-Domain-Policies"
	HeaderDNT                             = "DNT"
	HeaderTk                              = "Tk"
	HeaderContentDisposition              = "Content-Disposition"
	HeaderContentEncoding                 = "Content-Encoding"
	HeaderContentLanguage                 = "Content-Language"
	HeaderContentLength                   = "Content-Length"
	HeaderContentLocation                 = "Content-Location"
	HeaderContentType                     = "Content-Type"
	HeaderForwarded                       = "Forwarded"
	HeaderVia                             = "Via"
	HeaderXForwardedFor                   = "X-Forwarded-For"
	HeaderXForwardedHost                  = "X-Forwarded-Host"
	HeaderXForwardedProto                 = "X-Forwarded-Proto"
	HeaderXForwardedProtocol              = "X-Forwarded-Protocol"
	HeaderXForwardedSsl                   = "X-Forwarded-Ssl"
	HeaderXUrlScheme                      = "X-Url-Scheme"
	HeaderLocation                        = "Location"
	HeaderFrom                            = "From"
	HeaderHost                            = "Host"
	HeaderReferer                         = "Referer"
	HeaderReferrerPolicy                  = "Referrer-Policy"
	HeaderUserAgent                       = "User-Agent"
	HeaderAllow                           = "Allow"
	HeaderServer                          = "Server"
	HeaderAcceptRanges                    = "Accept-Ranges"
	HeaderContentRange                    = "Content-Range"
	HeaderIfRange                         = "If-Range"
	HeaderRange                           = "Range"
	HeaderContentSecurityPolicy           = "Content-Security-Policy"
	HeaderContentSecurityPolicyReportOnly = "Content-Security-Policy-Report-Only"
	HeaderCrossOriginResourcePolicy       = "Cross-Origin-Resource-Policy"
	HeaderExpectCT                        = "Expect-CT"
	HeaderFeaturePolicy                   = "Feature-Policy"
	HeaderPublicKeyPins                   = "Public-Key-Pins"
	HeaderPublicKeyPinsReportOnly         = "Public-Key-Pins-Report-Only"
	HeaderStrictTransportSecurity         = "Strict-Transport-Security"
	HeaderUpgradeInsecureRequests         = "Upgrade-Insecure-Requests"
	HeaderXContentTypeOptions             = "X-Content-Type-Options"
	HeaderXDownloadOptions                = "X-Download-Options"
	HeaderXFrameOptions                   = "X-Frame-Options"
	HeaderXPoweredBy                      = "X-Powered-By"
	HeaderXXSSProtection                  = "X-XSS-Protection"
	HeaderLastEventID                     = "Last-Event-ID"
	HeaderNEL                             = "NEL"
	HeaderPingFrom                        = "Ping-From"
	HeaderPingTo                          = "Ping-To"
	HeaderReportTo                        = "Report-To"
	HeaderTE                              = "TE"
	HeaderTrailer                         = "Trailer"
	HeaderTransferEncoding                = "Transfer-Encoding"
	HeaderSecWebSocketAccept              = "Sec-WebSocket-Accept"
	HeaderSecWebSocketExtensions          = "Sec-WebSocket-Extensions"
	HeaderSecWebSocketKey                 = "Sec-WebSocket-Key"
	HeaderSecWebSocketProtocol            = "Sec-WebSocket-Protocol"
	HeaderSecWebSocketVersion             = "Sec-WebSocket-Version"
	HeaderAcceptPatch                     = "Accept-Patch"
	HeaderAcceptPushPolicy                = "Accept-Push-Policy"
	HeaderAcceptSignature                 = "Accept-Signature"
	HeaderAltSvc                          = "Alt-Svc"
	HeaderDate                            = "Date"
	HeaderIndex                           = "Index"
	HeaderLargeAllocation                 = "Large-Allocation"
	HeaderLink                            = "Link"
	HeaderPushPolicy                      = "Push-Policy"
	HeaderRetryAfter                      = "Retry-After"
	HeaderServerTiming                    = "Server-Timing"
	HeaderSignature                       = "Signature"
	HeaderSignedHeaders                   = "Signed-Headers"
	HeaderSourceMap                       = "SourceMap"
	HeaderUpgrade                         = "Upgrade"
	HeaderXDNSPrefetchControl             = "X-DNS-Prefetch-Control"
	HeaderXPingback                       = "X-Pingback"
	HeaderXRequestID                      = "X-Request-ID"
	HeaderXRequestedWith                  = "X-Requested-With"
	HeaderXRobotsTag                      = "X-Robots-Tag"
	HeaderXUACompatible                   = "X-UA-Compatible"
)
