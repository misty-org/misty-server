import { createRequire as __wkfCreateRequire } from "node:module";
if (typeof globalThis.require === "undefined") globalThis.require = __wkfCreateRequire(import.meta.url);
import { r as __require, t as __commonJSMin } from "../_runtime.mjs";
import { c as require_src, l as require_ms } from "./@workflow/core+[...].mjs";
import { a as require_http_errors, i as require_on_finished, n as require_lib, o as require_statuses, r as require_type_is, s as require_depd, t as require_body_parser } from "./body-parser+[...].mjs";
import { n as require_mime_types, t as require_accepts } from "./accepts+[...].mjs";
import { t as require_encodeurl } from "./encodeurl.mjs";
import { t as require_escape_html } from "./escape-html.mjs";
import { t as require_content_type } from "./content-type.mjs";
import { t as require_etag } from "./etag.mjs";
import { t as require_content_disposition } from "./content-disposition.mjs";
import { t as require_cookie_signature } from "./cookie-signature.mjs";
import { t as require_cookie } from "./cookie.mjs";
//#region ../node_modules/merge-descriptors/index.js
var require_merge_descriptors = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	function mergeDescriptors(destination, source, overwrite = true) {
		if (!destination) throw new TypeError("The `destination` argument is required.");
		if (!source) throw new TypeError("The `source` argument is required.");
		for (const name of Object.getOwnPropertyNames(source)) {
			if (!overwrite && Object.hasOwn(destination, name)) continue;
			const descriptor = Object.getOwnPropertyDescriptor(source, name);
			Object.defineProperty(destination, name, descriptor);
		}
		return destination;
	}
	module.exports = mergeDescriptors;
}));
//#endregion
//#region ../node_modules/parseurl/index.js
/*!
* parseurl
* Copyright(c) 2014 Jonathan Ong
* Copyright(c) 2014-2017 Douglas Christopher Wilson
* MIT Licensed
*/
var require_parseurl = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var url$1 = __require("url");
	var parse = url$1.parse;
	var Url = url$1.Url;
	/**
	* Module exports.
	* @public
	*/
	module.exports = parseurl;
	module.exports.original = originalurl;
	/**
	* Parse the `req` url with memoization.
	*
	* @param {ServerRequest} req
	* @return {Object}
	* @public
	*/
	function parseurl(req) {
		var url = req.url;
		if (url === void 0) return;
		var parsed = req._parsedUrl;
		if (fresh(url, parsed)) return parsed;
		parsed = fastparse(url);
		parsed._raw = url;
		return req._parsedUrl = parsed;
	}
	/**
	* Parse the `req` original url with fallback and memoization.
	*
	* @param {ServerRequest} req
	* @return {Object}
	* @public
	*/
	function originalurl(req) {
		var url = req.originalUrl;
		if (typeof url !== "string") return parseurl(req);
		var parsed = req._parsedOriginalUrl;
		if (fresh(url, parsed)) return parsed;
		parsed = fastparse(url);
		parsed._raw = url;
		return req._parsedOriginalUrl = parsed;
	}
	/**
	* Parse the `str` url with fast-path short-cut.
	*
	* @param {string} str
	* @return {Object}
	* @private
	*/
	function fastparse(str) {
		if (typeof str !== "string" || str.charCodeAt(0) !== 47) return parse(str);
		var pathname = str;
		var query = null;
		var search = null;
		for (var i = 1; i < str.length; i++) switch (str.charCodeAt(i)) {
			case 63:
				if (search === null) {
					pathname = str.substring(0, i);
					query = str.substring(i + 1);
					search = str.substring(i);
				}
				break;
			case 9:
			case 10:
			case 12:
			case 13:
			case 32:
			case 35:
			case 160:
			case 65279: return parse(str);
		}
		var url = Url !== void 0 ? new Url() : {};
		url.path = str;
		url.href = str;
		url.pathname = pathname;
		if (search !== null) {
			url.query = query;
			url.search = search;
		}
		return url;
	}
	/**
	* Determine if parsed is still fresh for url.
	*
	* @param {string} url
	* @param {object} parsedUrl
	* @return {boolean}
	* @private
	*/
	function fresh(url, parsedUrl) {
		return typeof parsedUrl === "object" && parsedUrl !== null && (Url === void 0 || parsedUrl instanceof Url) && parsedUrl._raw === url;
	}
}));
//#endregion
//#region ../node_modules/finalhandler/index.js
/*!
* finalhandler
* Copyright(c) 2014-2022 Douglas Christopher Wilson
* MIT Licensed
*/
var require_finalhandler = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var debug = require_src()("finalhandler");
	var encodeUrl = require_encodeurl();
	var escapeHtml = require_escape_html();
	var onFinished = require_on_finished();
	var parseUrl = require_parseurl();
	var statuses = require_statuses();
	/**
	* Module variables.
	* @private
	*/
	var isFinished = onFinished.isFinished;
	/**
	* Create a minimal HTML document.
	*
	* @param {string} message
	* @private
	*/
	function createHtmlDocument(message) {
		return "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Error</title>\n</head>\n<body>\n<pre>" + escapeHtml(message).replaceAll("\n", "<br>").replaceAll("  ", " &nbsp;") + "</pre>\n</body>\n</html>\n";
	}
	/**
	* Module exports.
	* @public
	*/
	module.exports = finalhandler;
	/**
	* Create a function to handle the final response.
	*
	* @param {Request} req
	* @param {Response} res
	* @param {Object} [options]
	* @return {Function}
	* @public
	*/
	function finalhandler(req, res, options) {
		var opts = options || {};
		var env = opts.env || process.env.NODE_ENV || "development";
		var onerror = opts.onerror;
		return function(err) {
			var headers;
			var msg;
			var status;
			if (!err && res.headersSent) {
				debug("cannot 404 after headers sent");
				return;
			}
			if (err) {
				status = getErrorStatusCode(err);
				if (status === void 0) status = getResponseStatusCode(res);
				else headers = getErrorHeaders(err);
				msg = getErrorMessage(err, status, env);
			} else {
				status = 404;
				msg = "Cannot " + req.method + " " + encodeUrl(getResourceName(req));
			}
			debug("default %s", status);
			if (err && onerror) setImmediate(onerror, err, req, res);
			if (res.headersSent) {
				debug("cannot %d after headers sent", status);
				if (req.socket) req.socket.destroy();
				return;
			}
			send(req, res, status, headers, msg);
		};
	}
	/**
	* Get headers from Error object.
	*
	* @param {Error} err
	* @return {object}
	* @private
	*/
	function getErrorHeaders(err) {
		if (!err.headers || typeof err.headers !== "object") return;
		return { ...err.headers };
	}
	/**
	* Get message from Error object, fallback to status message.
	*
	* @param {Error} err
	* @param {number} status
	* @param {string} env
	* @return {string}
	* @private
	*/
	function getErrorMessage(err, status, env) {
		var msg;
		if (env !== "production") {
			msg = err.stack;
			if (!msg && typeof err.toString === "function") msg = err.toString();
		}
		return msg || statuses.message[status];
	}
	/**
	* Get status code from Error object.
	*
	* @param {Error} err
	* @return {number}
	* @private
	*/
	function getErrorStatusCode(err) {
		if (typeof err.status === "number" && err.status >= 400 && err.status < 600) return err.status;
		if (typeof err.statusCode === "number" && err.statusCode >= 400 && err.statusCode < 600) return err.statusCode;
	}
	/**
	* Get resource name for the request.
	*
	* This is typically just the original pathname of the request
	* but will fallback to "resource" is that cannot be determined.
	*
	* @param {IncomingMessage} req
	* @return {string}
	* @private
	*/
	function getResourceName(req) {
		try {
			return parseUrl.original(req).pathname;
		} catch (e) {
			return "resource";
		}
	}
	/**
	* Get status code from response.
	*
	* @param {OutgoingMessage} res
	* @return {number}
	* @private
	*/
	function getResponseStatusCode(res) {
		var status = res.statusCode;
		if (typeof status !== "number" || status < 400 || status > 599) status = 500;
		return status;
	}
	/**
	* Send response.
	*
	* @param {IncomingMessage} req
	* @param {OutgoingMessage} res
	* @param {number} status
	* @param {object} headers
	* @param {string} message
	* @private
	*/
	function send(req, res, status, headers, message) {
		function write() {
			var body = createHtmlDocument(message);
			res.statusCode = status;
			if (req.httpVersionMajor < 2) res.statusMessage = statuses.message[status];
			res.removeHeader("Content-Encoding");
			res.removeHeader("Content-Language");
			res.removeHeader("Content-Range");
			for (const [key, value] of Object.entries(headers ?? {})) res.setHeader(key, value);
			res.setHeader("Content-Security-Policy", "default-src 'none'");
			res.setHeader("X-Content-Type-Options", "nosniff");
			res.setHeader("Content-Type", "text/html; charset=utf-8");
			res.setHeader("Content-Length", Buffer.byteLength(body, "utf8"));
			if (req.method === "HEAD") {
				res.end();
				return;
			}
			res.end(body, "utf8");
		}
		if (isFinished(req)) {
			write();
			return;
		}
		req.unpipe();
		onFinished(req, write);
		req.resume();
	}
}));
//#endregion
//#region ../node_modules/express/lib/view.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_view = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var debug = require_src()("express:view");
	var path$2 = __require("node:path");
	var fs$1 = __require("node:fs");
	/**
	* Module variables.
	* @private
	*/
	var dirname = path$2.dirname;
	var basename = path$2.basename;
	var extname = path$2.extname;
	var join = path$2.join;
	var resolve = path$2.resolve;
	/**
	* Module exports.
	* @public
	*/
	module.exports = View;
	/**
	* Initialize a new `View` with the given `name`.
	*
	* Options:
	*
	*   - `defaultEngine` the default template engine name
	*   - `engines` template engine require() cache
	*   - `root` root path for view lookup
	*
	* @param {string} name
	* @param {object} options
	* @public
	*/
	function View(name, options) {
		var opts = options || {};
		this.defaultEngine = opts.defaultEngine;
		this.ext = extname(name);
		this.name = name;
		this.root = opts.root;
		if (!this.ext && !this.defaultEngine) throw new Error("No default engine was specified and no extension was provided.");
		var fileName = name;
		if (!this.ext) {
			this.ext = this.defaultEngine[0] !== "." ? "." + this.defaultEngine : this.defaultEngine;
			fileName += this.ext;
		}
		if (!opts.engines[this.ext]) {
			var mod = this.ext.slice(1);
			debug("require \"%s\"", mod);
			var fn = __require(mod).__express;
			if (typeof fn !== "function") throw new Error("Module \"" + mod + "\" does not provide a view engine.");
			opts.engines[this.ext] = fn;
		}
		this.engine = opts.engines[this.ext];
		this.path = this.lookup(fileName);
	}
	/**
	* Lookup view by the given `name`
	*
	* @param {string} name
	* @private
	*/
	View.prototype.lookup = function lookup(name) {
		var path;
		var roots = [].concat(this.root);
		debug("lookup \"%s\"", name);
		for (var i = 0; i < roots.length && !path; i++) {
			var root = roots[i];
			var loc = resolve(root, name);
			var dir = dirname(loc);
			var file = basename(loc);
			path = this.resolve(dir, file);
		}
		return path;
	};
	/**
	* Render with the given options.
	*
	* @param {object} options
	* @param {function} callback
	* @private
	*/
	View.prototype.render = function render(options, callback) {
		var sync = true;
		debug("render \"%s\"", this.path);
		this.engine(this.path, options, function onRender() {
			if (!sync) return callback.apply(this, arguments);
			var args = new Array(arguments.length);
			var cntx = this;
			for (var i = 0; i < arguments.length; i++) args[i] = arguments[i];
			return process.nextTick(function renderTick() {
				return callback.apply(cntx, args);
			});
		});
		sync = false;
	};
	/**
	* Resolve the file within the given directory.
	*
	* @param {string} dir
	* @param {string} file
	* @private
	*/
	View.prototype.resolve = function resolve(dir, file) {
		var ext = this.ext;
		var path = join(dir, file);
		var stat = tryStat(path);
		if (stat && stat.isFile()) return path;
		path = join(dir, basename(file, ext), "index" + ext);
		stat = tryStat(path);
		if (stat && stat.isFile()) return path;
	};
	/**
	* Return a stat, maybe.
	*
	* @param {string} path
	* @return {fs.Stats}
	* @private
	*/
	function tryStat(path) {
		debug("stat \"%s\"", path);
		try {
			return fs$1.statSync(path);
		} catch (e) {
			return;
		}
	}
}));
//#endregion
//#region ../node_modules/forwarded/index.js
/*!
* forwarded
* Copyright(c) 2014-2017 Douglas Christopher Wilson
* MIT Licensed
*/
var require_forwarded = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module exports.
	* @public
	*/
	module.exports = forwarded;
	/**
	* Get all addresses in the request, using the `X-Forwarded-For` header.
	*
	* @param {object} req
	* @return {array}
	* @public
	*/
	function forwarded(req) {
		if (!req) throw new TypeError("argument req is required");
		var proxyAddrs = parse(req.headers["x-forwarded-for"] || "");
		return [getSocketAddr(req)].concat(proxyAddrs);
	}
	/**
	* Get the socket address for a request.
	*
	* @param {object} req
	* @return {string}
	* @private
	*/
	function getSocketAddr(req) {
		return req.socket ? req.socket.remoteAddress : req.connection.remoteAddress;
	}
	/**
	* Parse the X-Forwarded-For header.
	*
	* @param {string} header
	* @private
	*/
	function parse(header) {
		var end = header.length;
		var list = [];
		var start = header.length;
		for (var i = header.length - 1; i >= 0; i--) switch (header.charCodeAt(i)) {
			case 32:
				if (start === end) start = end = i;
				break;
			case 44:
				if (start !== end) list.push(header.substring(start, end));
				start = end = i;
				break;
			default: start = i;
		}
		if (start !== end) list.push(header.substring(start, end));
		return list;
	}
}));
//#endregion
//#region ../node_modules/ipaddr.js/lib/ipaddr.js
var require_ipaddr = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	(function() {
		var expandIPv6, ipaddr = {}, ipv4Part, ipv4Regexes, ipv6Part, ipv6Regexes, matchCIDR, root = this, zoneIndex;
		if (typeof module !== "undefined" && module !== null && module.exports) module.exports = ipaddr;
		else root["ipaddr"] = ipaddr;
		matchCIDR = function(first, second, partSize, cidrBits) {
			var part, shift;
			if (first.length !== second.length) throw new Error("ipaddr: cannot match CIDR for objects with different lengths");
			part = 0;
			while (cidrBits > 0) {
				shift = partSize - cidrBits;
				if (shift < 0) shift = 0;
				if (first[part] >> shift !== second[part] >> shift) return false;
				cidrBits -= partSize;
				part += 1;
			}
			return true;
		};
		ipaddr.subnetMatch = function(address, rangeList, defaultName) {
			var k, len, rangeName, rangeSubnets, subnet;
			if (defaultName == null) defaultName = "unicast";
			for (rangeName in rangeList) {
				rangeSubnets = rangeList[rangeName];
				if (rangeSubnets[0] && !(rangeSubnets[0] instanceof Array)) rangeSubnets = [rangeSubnets];
				for (k = 0, len = rangeSubnets.length; k < len; k++) {
					subnet = rangeSubnets[k];
					if (address.kind() === subnet[0].kind()) {
						if (address.match.apply(address, subnet)) return rangeName;
					}
				}
			}
			return defaultName;
		};
		ipaddr.IPv4 = (function() {
			function IPv4(octets) {
				var k, len, octet;
				if (octets.length !== 4) throw new Error("ipaddr: ipv4 octet count should be 4");
				for (k = 0, len = octets.length; k < len; k++) {
					octet = octets[k];
					if (!(0 <= octet && octet <= 255)) throw new Error("ipaddr: ipv4 octet should fit in 8 bits");
				}
				this.octets = octets;
			}
			IPv4.prototype.kind = function() {
				return "ipv4";
			};
			IPv4.prototype.toString = function() {
				return this.octets.join(".");
			};
			IPv4.prototype.toNormalizedString = function() {
				return this.toString();
			};
			IPv4.prototype.toByteArray = function() {
				return this.octets.slice(0);
			};
			IPv4.prototype.match = function(other, cidrRange) {
				var ref;
				if (cidrRange === void 0) ref = other, other = ref[0], cidrRange = ref[1];
				if (other.kind() !== "ipv4") throw new Error("ipaddr: cannot match ipv4 address with non-ipv4 one");
				return matchCIDR(this.octets, other.octets, 8, cidrRange);
			};
			IPv4.prototype.SpecialRanges = {
				unspecified: [[new IPv4([
					0,
					0,
					0,
					0
				]), 8]],
				broadcast: [[new IPv4([
					255,
					255,
					255,
					255
				]), 32]],
				multicast: [[new IPv4([
					224,
					0,
					0,
					0
				]), 4]],
				linkLocal: [[new IPv4([
					169,
					254,
					0,
					0
				]), 16]],
				loopback: [[new IPv4([
					127,
					0,
					0,
					0
				]), 8]],
				carrierGradeNat: [[new IPv4([
					100,
					64,
					0,
					0
				]), 10]],
				"private": [
					[new IPv4([
						10,
						0,
						0,
						0
					]), 8],
					[new IPv4([
						172,
						16,
						0,
						0
					]), 12],
					[new IPv4([
						192,
						168,
						0,
						0
					]), 16]
				],
				reserved: [
					[new IPv4([
						192,
						0,
						0,
						0
					]), 24],
					[new IPv4([
						192,
						0,
						2,
						0
					]), 24],
					[new IPv4([
						192,
						88,
						99,
						0
					]), 24],
					[new IPv4([
						198,
						51,
						100,
						0
					]), 24],
					[new IPv4([
						203,
						0,
						113,
						0
					]), 24],
					[new IPv4([
						240,
						0,
						0,
						0
					]), 4]
				]
			};
			IPv4.prototype.range = function() {
				return ipaddr.subnetMatch(this, this.SpecialRanges);
			};
			IPv4.prototype.toIPv4MappedAddress = function() {
				return ipaddr.IPv6.parse("::ffff:" + this.toString());
			};
			IPv4.prototype.prefixLengthFromSubnetMask = function() {
				var cidr, i, k, octet, stop, zeros, zerotable = {
					0: 8,
					128: 7,
					192: 6,
					224: 5,
					240: 4,
					248: 3,
					252: 2,
					254: 1,
					255: 0
				};
				cidr = 0;
				stop = false;
				for (i = k = 3; k >= 0; i = k += -1) {
					octet = this.octets[i];
					if (octet in zerotable) {
						zeros = zerotable[octet];
						if (stop && zeros !== 0) return null;
						if (zeros !== 8) stop = true;
						cidr += zeros;
					} else return null;
				}
				return 32 - cidr;
			};
			return IPv4;
		})();
		ipv4Part = "(0?\\d+|0x[a-f0-9]+)";
		ipv4Regexes = {
			fourOctet: new RegExp("^" + ipv4Part + "\\." + ipv4Part + "\\." + ipv4Part + "\\." + ipv4Part + "$", "i"),
			longValue: new RegExp("^" + ipv4Part + "$", "i")
		};
		ipaddr.IPv4.parser = function(string) {
			var match, parseIntAuto = function(string) {
				if (string[0] === "0" && string[1] !== "x") return parseInt(string, 8);
				else return parseInt(string);
			}, part, shift, value;
			if (match = string.match(ipv4Regexes.fourOctet)) return (function() {
				var k, len, ref = match.slice(1, 6), results = [];
				for (k = 0, len = ref.length; k < len; k++) {
					part = ref[k];
					results.push(parseIntAuto(part));
				}
				return results;
			})();
			else if (match = string.match(ipv4Regexes.longValue)) {
				value = parseIntAuto(match[1]);
				if (value > 4294967295 || value < 0) throw new Error("ipaddr: address outside defined range");
				return (function() {
					var k, results = [];
					for (shift = k = 0; k <= 24; shift = k += 8) results.push(value >> shift & 255);
					return results;
				})().reverse();
			} else return null;
		};
		ipaddr.IPv6 = (function() {
			function IPv6(parts, zoneId) {
				var i, k, l, len, part, ref;
				if (parts.length === 16) {
					this.parts = [];
					for (i = k = 0; k <= 14; i = k += 2) this.parts.push(parts[i] << 8 | parts[i + 1]);
				} else if (parts.length === 8) this.parts = parts;
				else throw new Error("ipaddr: ipv6 part count should be 8 or 16");
				ref = this.parts;
				for (l = 0, len = ref.length; l < len; l++) {
					part = ref[l];
					if (!(0 <= part && part <= 65535)) throw new Error("ipaddr: ipv6 part should fit in 16 bits");
				}
				if (zoneId) this.zoneId = zoneId;
			}
			IPv6.prototype.kind = function() {
				return "ipv6";
			};
			IPv6.prototype.toString = function() {
				return this.toNormalizedString().replace(/((^|:)(0(:|$))+)/, "::");
			};
			IPv6.prototype.toRFC5952String = function() {
				var bestMatchIndex, bestMatchLength, match, regex = /((^|:)(0(:|$)){2,})/g, string = this.toNormalizedString();
				bestMatchIndex = 0;
				bestMatchLength = -1;
				while (match = regex.exec(string)) if (match[0].length > bestMatchLength) {
					bestMatchIndex = match.index;
					bestMatchLength = match[0].length;
				}
				if (bestMatchLength < 0) return string;
				return string.substring(0, bestMatchIndex) + "::" + string.substring(bestMatchIndex + bestMatchLength);
			};
			IPv6.prototype.toByteArray = function() {
				var bytes = [], k, len, part, ref = this.parts;
				for (k = 0, len = ref.length; k < len; k++) {
					part = ref[k];
					bytes.push(part >> 8);
					bytes.push(part & 255);
				}
				return bytes;
			};
			IPv6.prototype.toNormalizedString = function() {
				var addr = (function() {
					var k, len, ref = this.parts, results = [];
					for (k = 0, len = ref.length; k < len; k++) {
						part = ref[k];
						results.push(part.toString(16));
					}
					return results;
				}).call(this).join(":"), part, suffix = "";
				if (this.zoneId) suffix = "%" + this.zoneId;
				return addr + suffix;
			};
			IPv6.prototype.toFixedLengthString = function() {
				var addr = (function() {
					var k, len, ref = this.parts, results = [];
					for (k = 0, len = ref.length; k < len; k++) {
						part = ref[k];
						results.push(part.toString(16).padStart(4, "0"));
					}
					return results;
				}).call(this).join(":"), part, suffix = "";
				if (this.zoneId) suffix = "%" + this.zoneId;
				return addr + suffix;
			};
			IPv6.prototype.match = function(other, cidrRange) {
				var ref;
				if (cidrRange === void 0) ref = other, other = ref[0], cidrRange = ref[1];
				if (other.kind() !== "ipv6") throw new Error("ipaddr: cannot match ipv6 address with non-ipv6 one");
				return matchCIDR(this.parts, other.parts, 16, cidrRange);
			};
			IPv6.prototype.SpecialRanges = {
				unspecified: [new IPv6([
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 128],
				linkLocal: [new IPv6([
					65152,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 10],
				multicast: [new IPv6([
					65280,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 8],
				loopback: [new IPv6([
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					1
				]), 128],
				uniqueLocal: [new IPv6([
					64512,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 7],
				ipv4Mapped: [new IPv6([
					0,
					0,
					0,
					0,
					0,
					65535,
					0,
					0
				]), 96],
				rfc6145: [new IPv6([
					0,
					0,
					0,
					0,
					65535,
					0,
					0,
					0
				]), 96],
				rfc6052: [new IPv6([
					100,
					65435,
					0,
					0,
					0,
					0,
					0,
					0
				]), 96],
				"6to4": [new IPv6([
					8194,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 16],
				teredo: [new IPv6([
					8193,
					0,
					0,
					0,
					0,
					0,
					0,
					0
				]), 32],
				reserved: [[new IPv6([
					8193,
					3512,
					0,
					0,
					0,
					0,
					0,
					0
				]), 32]]
			};
			IPv6.prototype.range = function() {
				return ipaddr.subnetMatch(this, this.SpecialRanges);
			};
			IPv6.prototype.isIPv4MappedAddress = function() {
				return this.range() === "ipv4Mapped";
			};
			IPv6.prototype.toIPv4Address = function() {
				var high, low, ref;
				if (!this.isIPv4MappedAddress()) throw new Error("ipaddr: trying to convert a generic ipv6 address to ipv4");
				ref = this.parts.slice(-2), high = ref[0], low = ref[1];
				return new ipaddr.IPv4([
					high >> 8,
					high & 255,
					low >> 8,
					low & 255
				]);
			};
			IPv6.prototype.prefixLengthFromSubnetMask = function() {
				var cidr, i, k, part, stop, zeros, zerotable = {
					0: 16,
					32768: 15,
					49152: 14,
					57344: 13,
					61440: 12,
					63488: 11,
					64512: 10,
					65024: 9,
					65280: 8,
					65408: 7,
					65472: 6,
					65504: 5,
					65520: 4,
					65528: 3,
					65532: 2,
					65534: 1,
					65535: 0
				};
				cidr = 0;
				stop = false;
				for (i = k = 7; k >= 0; i = k += -1) {
					part = this.parts[i];
					if (part in zerotable) {
						zeros = zerotable[part];
						if (stop && zeros !== 0) return null;
						if (zeros !== 16) stop = true;
						cidr += zeros;
					} else return null;
				}
				return 128 - cidr;
			};
			return IPv6;
		})();
		ipv6Part = "(?:[0-9a-f]+::?)+";
		zoneIndex = "%[0-9a-z]{1,}";
		ipv6Regexes = {
			zoneIndex: new RegExp(zoneIndex, "i"),
			"native": new RegExp("^(::)?(" + ipv6Part + ")?([0-9a-f]+)?(::)?(" + zoneIndex + ")?$", "i"),
			transitional: new RegExp("^((?:" + ipv6Part + ")|(?:::)(?:" + ipv6Part + ")?)" + (ipv4Part + "\\." + ipv4Part + "\\." + ipv4Part + "\\." + ipv4Part) + ("(" + zoneIndex + ")?$"), "i")
		};
		expandIPv6 = function(string, parts) {
			var colonCount, lastColon, part, replacement, replacementCount, zoneId;
			if (string.indexOf("::") !== string.lastIndexOf("::")) return null;
			zoneId = (string.match(ipv6Regexes["zoneIndex"]) || [])[0];
			if (zoneId) {
				zoneId = zoneId.substring(1);
				string = string.replace(/%.+$/, "");
			}
			colonCount = 0;
			lastColon = -1;
			while ((lastColon = string.indexOf(":", lastColon + 1)) >= 0) colonCount++;
			if (string.substr(0, 2) === "::") colonCount--;
			if (string.substr(-2, 2) === "::") colonCount--;
			if (colonCount > parts) return null;
			replacementCount = parts - colonCount;
			replacement = ":";
			while (replacementCount--) replacement += "0:";
			string = string.replace("::", replacement);
			if (string[0] === ":") string = string.slice(1);
			if (string[string.length - 1] === ":") string = string.slice(0, -1);
			parts = (function() {
				var k, len, ref = string.split(":"), results = [];
				for (k = 0, len = ref.length; k < len; k++) {
					part = ref[k];
					results.push(parseInt(part, 16));
				}
				return results;
			})();
			return {
				parts,
				zoneId
			};
		};
		ipaddr.IPv6.parser = function(string) {
			var addr, k, len, match, octet, octets, zoneId;
			if (ipv6Regexes["native"].test(string)) return expandIPv6(string, 8);
			else if (match = string.match(ipv6Regexes["transitional"])) {
				zoneId = match[6] || "";
				addr = expandIPv6(match[1].slice(0, -1) + zoneId, 6);
				if (addr.parts) {
					octets = [
						parseInt(match[2]),
						parseInt(match[3]),
						parseInt(match[4]),
						parseInt(match[5])
					];
					for (k = 0, len = octets.length; k < len; k++) {
						octet = octets[k];
						if (!(0 <= octet && octet <= 255)) return null;
					}
					addr.parts.push(octets[0] << 8 | octets[1]);
					addr.parts.push(octets[2] << 8 | octets[3]);
					return {
						parts: addr.parts,
						zoneId: addr.zoneId
					};
				}
			}
			return null;
		};
		ipaddr.IPv4.isIPv4 = ipaddr.IPv6.isIPv6 = function(string) {
			return this.parser(string) !== null;
		};
		ipaddr.IPv4.isValid = function(string) {
			try {
				new this(this.parser(string));
				return true;
			} catch (error1) {
				return false;
			}
		};
		ipaddr.IPv4.isValidFourPartDecimal = function(string) {
			if (ipaddr.IPv4.isValid(string) && string.match(/^(0|[1-9]\d*)(\.(0|[1-9]\d*)){3}$/)) return true;
			else return false;
		};
		ipaddr.IPv6.isValid = function(string) {
			var addr;
			if (typeof string === "string" && string.indexOf(":") === -1) return false;
			try {
				addr = this.parser(string);
				new this(addr.parts, addr.zoneId);
				return true;
			} catch (error1) {
				return false;
			}
		};
		ipaddr.IPv4.parse = function(string) {
			var parts = this.parser(string);
			if (parts === null) throw new Error("ipaddr: string is not formatted like ip address");
			return new this(parts);
		};
		ipaddr.IPv6.parse = function(string) {
			var addr = this.parser(string);
			if (addr.parts === null) throw new Error("ipaddr: string is not formatted like ip address");
			return new this(addr.parts, addr.zoneId);
		};
		ipaddr.IPv4.parseCIDR = function(string) {
			var maskLength, match, parsed;
			if (match = string.match(/^(.+)\/(\d+)$/)) {
				maskLength = parseInt(match[2]);
				if (maskLength >= 0 && maskLength <= 32) {
					parsed = [this.parse(match[1]), maskLength];
					Object.defineProperty(parsed, "toString", { value: function() {
						return this.join("/");
					} });
					return parsed;
				}
			}
			throw new Error("ipaddr: string is not formatted like an IPv4 CIDR range");
		};
		ipaddr.IPv4.subnetMaskFromPrefixLength = function(prefix) {
			var filledOctetCount, j, octets;
			prefix = parseInt(prefix);
			if (prefix < 0 || prefix > 32) throw new Error("ipaddr: invalid IPv4 prefix length");
			octets = [
				0,
				0,
				0,
				0
			];
			j = 0;
			filledOctetCount = Math.floor(prefix / 8);
			while (j < filledOctetCount) {
				octets[j] = 255;
				j++;
			}
			if (filledOctetCount < 4) octets[filledOctetCount] = Math.pow(2, prefix % 8) - 1 << 8 - prefix % 8;
			return new this(octets);
		};
		ipaddr.IPv4.broadcastAddressFromCIDR = function(string) {
			var cidr, i, ipInterfaceOctets, octets, subnetMaskOctets;
			try {
				cidr = this.parseCIDR(string);
				ipInterfaceOctets = cidr[0].toByteArray();
				subnetMaskOctets = this.subnetMaskFromPrefixLength(cidr[1]).toByteArray();
				octets = [];
				i = 0;
				while (i < 4) {
					octets.push(parseInt(ipInterfaceOctets[i], 10) | parseInt(subnetMaskOctets[i], 10) ^ 255);
					i++;
				}
				return new this(octets);
			} catch (error1) {
				throw new Error("ipaddr: the address does not have IPv4 CIDR format");
			}
		};
		ipaddr.IPv4.networkAddressFromCIDR = function(string) {
			var cidr, i, ipInterfaceOctets, octets, subnetMaskOctets;
			try {
				cidr = this.parseCIDR(string);
				ipInterfaceOctets = cidr[0].toByteArray();
				subnetMaskOctets = this.subnetMaskFromPrefixLength(cidr[1]).toByteArray();
				octets = [];
				i = 0;
				while (i < 4) {
					octets.push(parseInt(ipInterfaceOctets[i], 10) & parseInt(subnetMaskOctets[i], 10));
					i++;
				}
				return new this(octets);
			} catch (error1) {
				throw new Error("ipaddr: the address does not have IPv4 CIDR format");
			}
		};
		ipaddr.IPv6.parseCIDR = function(string) {
			var maskLength, match, parsed;
			if (match = string.match(/^(.+)\/(\d+)$/)) {
				maskLength = parseInt(match[2]);
				if (maskLength >= 0 && maskLength <= 128) {
					parsed = [this.parse(match[1]), maskLength];
					Object.defineProperty(parsed, "toString", { value: function() {
						return this.join("/");
					} });
					return parsed;
				}
			}
			throw new Error("ipaddr: string is not formatted like an IPv6 CIDR range");
		};
		ipaddr.isValid = function(string) {
			return ipaddr.IPv6.isValid(string) || ipaddr.IPv4.isValid(string);
		};
		ipaddr.parse = function(string) {
			if (ipaddr.IPv6.isValid(string)) return ipaddr.IPv6.parse(string);
			else if (ipaddr.IPv4.isValid(string)) return ipaddr.IPv4.parse(string);
			else throw new Error("ipaddr: the address has neither IPv6 nor IPv4 format");
		};
		ipaddr.parseCIDR = function(string) {
			try {
				return ipaddr.IPv6.parseCIDR(string);
			} catch (error1) {
				try {
					return ipaddr.IPv4.parseCIDR(string);
				} catch (error1) {
					throw new Error("ipaddr: the address has neither IPv6 nor IPv4 CIDR format");
				}
			}
		};
		ipaddr.fromByteArray = function(bytes) {
			var length = bytes.length;
			if (length === 4) return new ipaddr.IPv4(bytes);
			else if (length === 16) return new ipaddr.IPv6(bytes);
			else throw new Error("ipaddr: the binary input is neither an IPv6 nor IPv4 address");
		};
		ipaddr.process = function(string) {
			var addr = this.parse(string);
			if (addr.kind() === "ipv6" && addr.isIPv4MappedAddress()) return addr.toIPv4Address();
			else return addr;
		};
	}).call(exports);
}));
//#endregion
//#region ../node_modules/proxy-addr/index.js
/*!
* proxy-addr
* Copyright(c) 2014-2016 Douglas Christopher Wilson
* MIT Licensed
*/
var require_proxy_addr = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module exports.
	* @public
	*/
	module.exports = proxyaddr;
	module.exports.all = alladdrs;
	module.exports.compile = compile;
	/**
	* Module dependencies.
	* @private
	*/
	var forwarded = require_forwarded();
	var ipaddr = require_ipaddr();
	/**
	* Variables.
	* @private
	*/
	var DIGIT_REGEXP = /^[0-9]+$/;
	var isip = ipaddr.isValid;
	var parseip = ipaddr.parse;
	/**
	* Pre-defined IP ranges.
	* @private
	*/
	var IP_RANGES = {
		linklocal: ["169.254.0.0/16", "fe80::/10"],
		loopback: ["127.0.0.1/8", "::1/128"],
		uniquelocal: [
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7"
		]
	};
	/**
	* Get all addresses in the request, optionally stopping
	* at the first untrusted.
	*
	* @param {Object} request
	* @param {Function|Array|String} [trust]
	* @public
	*/
	function alladdrs(req, trust) {
		var addrs = forwarded(req);
		if (!trust) return addrs;
		if (typeof trust !== "function") trust = compile(trust);
		for (var i = 0; i < addrs.length - 1; i++) {
			if (trust(addrs[i], i)) continue;
			addrs.length = i + 1;
		}
		return addrs;
	}
	/**
	* Compile argument into trust function.
	*
	* @param {Array|String} val
	* @private
	*/
	function compile(val) {
		if (!val) throw new TypeError("argument is required");
		var trust;
		if (typeof val === "string") trust = [val];
		else if (Array.isArray(val)) trust = val.slice();
		else throw new TypeError("unsupported trust argument");
		for (var i = 0; i < trust.length; i++) {
			val = trust[i];
			if (!Object.prototype.hasOwnProperty.call(IP_RANGES, val)) continue;
			val = IP_RANGES[val];
			trust.splice.apply(trust, [i, 1].concat(val));
			i += val.length - 1;
		}
		return compileTrust(compileRangeSubnets(trust));
	}
	/**
	* Compile `arr` elements into range subnets.
	*
	* @param {Array} arr
	* @private
	*/
	function compileRangeSubnets(arr) {
		var rangeSubnets = new Array(arr.length);
		for (var i = 0; i < arr.length; i++) rangeSubnets[i] = parseipNotation(arr[i]);
		return rangeSubnets;
	}
	/**
	* Compile range subnet array into trust function.
	*
	* @param {Array} rangeSubnets
	* @private
	*/
	function compileTrust(rangeSubnets) {
		var len = rangeSubnets.length;
		return len === 0 ? trustNone : len === 1 ? trustSingle(rangeSubnets[0]) : trustMulti(rangeSubnets);
	}
	/**
	* Parse IP notation string into range subnet.
	*
	* @param {String} note
	* @private
	*/
	function parseipNotation(note) {
		var pos = note.lastIndexOf("/");
		var str = pos !== -1 ? note.substring(0, pos) : note;
		if (!isip(str)) throw new TypeError("invalid IP address: " + str);
		var ip = parseip(str);
		if (pos === -1 && ip.kind() === "ipv6" && ip.isIPv4MappedAddress()) ip = ip.toIPv4Address();
		var max = ip.kind() === "ipv6" ? 128 : 32;
		var range = pos !== -1 ? note.substring(pos + 1, note.length) : null;
		if (range === null) range = max;
		else if (DIGIT_REGEXP.test(range)) range = parseInt(range, 10);
		else if (ip.kind() === "ipv4" && isip(range)) range = parseNetmask(range);
		else range = null;
		if (range <= 0 || range > max) throw new TypeError("invalid range on address: " + note);
		return [ip, range];
	}
	/**
	* Parse netmask string into CIDR range.
	*
	* @param {String} netmask
	* @private
	*/
	function parseNetmask(netmask) {
		var ip = parseip(netmask);
		return ip.kind() === "ipv4" ? ip.prefixLengthFromSubnetMask() : null;
	}
	/**
	* Determine address of proxied request.
	*
	* @param {Object} request
	* @param {Function|Array|String} trust
	* @public
	*/
	function proxyaddr(req, trust) {
		if (!req) throw new TypeError("req argument is required");
		if (!trust) throw new TypeError("trust argument is required");
		var addrs = alladdrs(req, trust);
		return addrs[addrs.length - 1];
	}
	/**
	* Static trust function to trust nothing.
	*
	* @private
	*/
	function trustNone() {
		return false;
	}
	/**
	* Compile trust function for multiple subnets.
	*
	* @param {Array} subnets
	* @private
	*/
	function trustMulti(subnets) {
		return function trust(addr) {
			if (!isip(addr)) return false;
			var ip = parseip(addr);
			var ipconv;
			var kind = ip.kind();
			for (var i = 0; i < subnets.length; i++) {
				var subnet = subnets[i];
				var subnetip = subnet[0];
				var subnetkind = subnetip.kind();
				var subnetrange = subnet[1];
				var trusted = ip;
				if (kind !== subnetkind) {
					if (subnetkind === "ipv4" && !ip.isIPv4MappedAddress()) continue;
					if (!ipconv) ipconv = subnetkind === "ipv4" ? ip.toIPv4Address() : ip.toIPv4MappedAddress();
					trusted = ipconv;
				}
				if (trusted.match(subnetip, subnetrange)) return true;
			}
			return false;
		};
	}
	/**
	* Compile trust function for single subnet.
	*
	* @param {Object} subnet
	* @private
	*/
	function trustSingle(subnet) {
		var subnetip = subnet[0];
		var subnetkind = subnetip.kind();
		var subnetisipv4 = subnetkind === "ipv4";
		var subnetrange = subnet[1];
		return function trust(addr) {
			if (!isip(addr)) return false;
			var ip = parseip(addr);
			if (ip.kind() !== subnetkind) {
				if (subnetisipv4 && !ip.isIPv4MappedAddress()) return false;
				ip = subnetisipv4 ? ip.toIPv4Address() : ip.toIPv4MappedAddress();
			}
			return ip.match(subnetip, subnetrange);
		};
	}
}));
//#endregion
//#region ../node_modules/express/lib/utils.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_utils = /* @__PURE__ */ __commonJSMin(((exports) => {
	/**
	* Module dependencies.
	* @api private
	*/
	var { METHODS: METHODS$2 } = __require("node:http");
	var contentType = require_content_type();
	var etag = require_etag();
	var mime = require_mime_types();
	var proxyaddr = require_proxy_addr();
	var qs = require_lib();
	var querystring = __require("node:querystring");
	const { Buffer: Buffer$2 } = __require("node:buffer");
	/**
	* A list of lowercased HTTP methods that are supported by Node.js.
	* @api private
	*/
	exports.methods = METHODS$2.map((method) => method.toLowerCase());
	/**
	* Return strong ETag for `body`.
	*
	* @param {String|Buffer} body
	* @param {String} [encoding]
	* @return {String}
	* @api private
	*/
	exports.etag = createETagGenerator({ weak: false });
	/**
	* Return weak ETag for `body`.
	*
	* @param {String|Buffer} body
	* @param {String} [encoding]
	* @return {String}
	* @api private
	*/
	exports.wetag = createETagGenerator({ weak: true });
	/**
	* Normalize the given `type`, for example "html" becomes "text/html".
	*
	* @param {String} type
	* @return {Object}
	* @api private
	*/
	exports.normalizeType = function(type) {
		return ~type.indexOf("/") ? acceptParams(type) : {
			value: mime.lookup(type) || "application/octet-stream",
			params: {}
		};
	};
	/**
	* Normalize `types`, for example "html" becomes "text/html".
	*
	* @param {Array} types
	* @return {Array}
	* @api private
	*/
	exports.normalizeTypes = function(types) {
		return types.map(exports.normalizeType);
	};
	/**
	* Parse accept params `str` returning an
	* object with `.value`, `.quality` and `.params`.
	*
	* @param {String} str
	* @return {Object}
	* @api private
	*/
	function acceptParams(str) {
		var length = str.length;
		var colonIndex = str.indexOf(";");
		var index = colonIndex === -1 ? length : colonIndex;
		var ret = {
			value: str.slice(0, index).trim(),
			quality: 1,
			params: {}
		};
		while (index < length) {
			var splitIndex = str.indexOf("=", index);
			if (splitIndex === -1) break;
			var colonIndex = str.indexOf(";", index);
			var endIndex = colonIndex === -1 ? length : colonIndex;
			if (splitIndex > endIndex) {
				index = str.lastIndexOf(";", splitIndex - 1) + 1;
				continue;
			}
			var key = str.slice(index, splitIndex).trim();
			var value = str.slice(splitIndex + 1, endIndex).trim();
			if (key === "q") ret.quality = parseFloat(value);
			else ret.params[key] = value;
			index = endIndex + 1;
		}
		return ret;
	}
	/**
	* Compile "etag" value to function.
	*
	* @param  {Boolean|String|Function} val
	* @return {Function}
	* @api private
	*/
	exports.compileETag = function(val) {
		var fn;
		if (typeof val === "function") return val;
		switch (val) {
			case true:
			case "weak":
				fn = exports.wetag;
				break;
			case false: break;
			case "strong":
				fn = exports.etag;
				break;
			default: throw new TypeError("unknown value for etag function: " + val);
		}
		return fn;
	};
	/**
	* Compile "query parser" value to function.
	*
	* @param  {String|Function} val
	* @return {Function}
	* @api private
	*/
	exports.compileQueryParser = function compileQueryParser(val) {
		var fn;
		if (typeof val === "function") return val;
		switch (val) {
			case true:
			case "simple":
				fn = querystring.parse;
				break;
			case false: break;
			case "extended":
				fn = parseExtendedQueryString;
				break;
			default: throw new TypeError("unknown value for query parser function: " + val);
		}
		return fn;
	};
	/**
	* Compile "proxy trust" value to function.
	*
	* @param  {Boolean|String|Number|Array|Function} val
	* @return {Function}
	* @api private
	*/
	exports.compileTrust = function(val) {
		if (typeof val === "function") return val;
		if (val === true) return function() {
			return true;
		};
		if (typeof val === "number") return function(a, i) {
			return i < val;
		};
		if (typeof val === "string") val = val.split(",").map(function(v) {
			return v.trim();
		});
		return proxyaddr.compile(val || []);
	};
	/**
	* Set the charset in a given Content-Type string.
	*
	* @param {String} type
	* @param {String} charset
	* @return {String}
	* @api private
	*/
	exports.setCharset = function setCharset(type, charset) {
		if (!type || !charset) return type;
		var parsed = contentType.parse(type);
		parsed.parameters.charset = charset;
		return contentType.format(parsed);
	};
	/**
	* Create an ETag generator function, generating ETags with
	* the given options.
	*
	* @param {object} options
	* @return {function}
	* @private
	*/
	function createETagGenerator(options) {
		return function generateETag(body, encoding) {
			return etag(!Buffer$2.isBuffer(body) ? Buffer$2.from(body, encoding) : body, options);
		};
	}
	/**
	* Parse an extended query string with qs.
	*
	* @param {String} str
	* @return {Object}
	* @private
	*/
	function parseExtendedQueryString(str) {
		return qs.parse(str, { allowPrototypes: true });
	}
}));
//#endregion
//#region ../node_modules/wrappy/wrappy.js
var require_wrappy = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	module.exports = wrappy;
	function wrappy(fn, cb) {
		if (fn && cb) return wrappy(fn)(cb);
		if (typeof fn !== "function") throw new TypeError("need wrapper function");
		Object.keys(fn).forEach(function(k) {
			wrapper[k] = fn[k];
		});
		return wrapper;
		function wrapper() {
			var args = new Array(arguments.length);
			for (var i = 0; i < args.length; i++) args[i] = arguments[i];
			var ret = fn.apply(this, args);
			var cb = args[args.length - 1];
			if (typeof ret === "function" && ret !== cb) Object.keys(cb).forEach(function(k) {
				ret[k] = cb[k];
			});
			return ret;
		}
	}
}));
//#endregion
//#region ../node_modules/once/once.js
var require_once = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var wrappy = require_wrappy();
	module.exports = wrappy(once);
	module.exports.strict = wrappy(onceStrict);
	once.proto = once(function() {
		Object.defineProperty(Function.prototype, "once", {
			value: function() {
				return once(this);
			},
			configurable: true
		});
		Object.defineProperty(Function.prototype, "onceStrict", {
			value: function() {
				return onceStrict(this);
			},
			configurable: true
		});
	});
	function once(fn) {
		var f = function() {
			if (f.called) return f.value;
			f.called = true;
			return f.value = fn.apply(this, arguments);
		};
		f.called = false;
		return f;
	}
	function onceStrict(fn) {
		var f = function() {
			if (f.called) throw new Error(f.onceError);
			f.called = true;
			return f.value = fn.apply(this, arguments);
		};
		f.onceError = (fn.name || "Function wrapped with `once`") + " shouldn't be called more than once";
		f.called = false;
		return f;
	}
}));
//#endregion
//#region ../node_modules/is-promise/index.js
var require_is_promise = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	module.exports = isPromise;
	module.exports.default = isPromise;
	function isPromise(obj) {
		return !!obj && (typeof obj === "object" || typeof obj === "function") && typeof obj.then === "function";
	}
}));
//#endregion
//#region ../node_modules/path-to-regexp/dist/index.js
var require_dist = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.PathError = exports.TokenData = void 0;
	exports.parse = parse;
	exports.compile = compile;
	exports.match = match;
	exports.pathToRegexp = pathToRegexp;
	exports.stringify = stringify;
	const DEFAULT_DELIMITER = "/";
	const NOOP_VALUE = (value) => value;
	const ID_START = /^[$_\p{ID_Start}]$/u;
	const ID_CONTINUE = /^[$\u200c\u200d\p{ID_Continue}]$/u;
	const ID = /^[$_\p{ID_Start}][$\u200c\u200d\p{ID_Continue}]*$/u;
	/**
	* Escape text for stringify to path.
	*/
	function escapeText(str) {
		return str.replace(/[{}()\[\]+?!:*\\]/g, "\\$&");
	}
	/**
	* Escape a regular expression string.
	*/
	function escape(str) {
		return str.replace(/[.+*?^${}()[\]|/\\]/g, "\\$&");
	}
	/**
	* Tokenized path instance.
	*/
	var TokenData = class {
		constructor(tokens, originalPath) {
			this.tokens = tokens;
			this.originalPath = originalPath;
		}
	};
	exports.TokenData = TokenData;
	/**
	* ParseError is thrown when there is an error processing the path.
	*/
	var PathError = class extends TypeError {
		constructor(message, originalPath) {
			let text = message;
			if (originalPath) text += `: ${originalPath}`;
			text += `; visit https://git.new/pathToRegexpError for info`;
			super(text);
			this.originalPath = originalPath;
		}
	};
	exports.PathError = PathError;
	/**
	* Parse a string for the raw tokens.
	*/
	function parse(str, options = {}) {
		const { encodePath = NOOP_VALUE } = options;
		const chars = [...str];
		let index = 0;
		function consumeUntil(end) {
			const output = [];
			let path = "";
			function writePath() {
				if (!path) return;
				output.push({
					type: "text",
					value: encodePath(path)
				});
				path = "";
			}
			while (index < chars.length) {
				const value = chars[index++];
				if (value === end) {
					writePath();
					return output;
				}
				if (value === "\\") {
					if (index === chars.length) throw new PathError(`Unexpected end after \\ at index ${index}`, str);
					path += chars[index++];
					continue;
				}
				if (value === ":" || value === "*") {
					const type = value === ":" ? "param" : "wildcard";
					let name = "";
					if (ID_START.test(chars[index])) do
						name += chars[index++];
					while (ID_CONTINUE.test(chars[index]));
					else if (chars[index] === "\"") {
						let quoteStart = index;
						while (index < chars.length) {
							if (chars[++index] === "\"") {
								index++;
								quoteStart = 0;
								break;
							}
							if (chars[index] === "\\") index++;
							name += chars[index];
						}
						if (quoteStart) throw new PathError(`Unterminated quote at index ${quoteStart}`, str);
					}
					if (!name) throw new PathError(`Missing parameter name at index ${index}`, str);
					writePath();
					output.push({
						type,
						name
					});
					continue;
				}
				if (value === "{") {
					writePath();
					output.push({
						type: "group",
						tokens: consumeUntil("}")
					});
					continue;
				}
				if (value === "}" || value === "(" || value === ")" || value === "[" || value === "]" || value === "+" || value === "?" || value === "!") throw new PathError(`Unexpected ${value} at index ${index - 1}`, str);
				path += value;
			}
			if (end) throw new PathError(`Unexpected end at index ${index}, expected ${end}`, str);
			writePath();
			return output;
		}
		return new TokenData(consumeUntil(""), str);
	}
	/**
	* Compile a string to a template function for the path.
	*/
	function compile(path, options = {}) {
		const { encode = encodeURIComponent, delimiter = DEFAULT_DELIMITER } = options;
		const fn = tokensToFunction((typeof path === "object" ? path : parse(path, options)).tokens, delimiter, encode);
		return function path(params = {}) {
			const missing = [];
			const path = fn(params, missing);
			if (missing.length) throw new TypeError(`Missing parameters: ${missing.join(", ")}`);
			return path;
		};
	}
	function tokensToFunction(tokens, delimiter, encode) {
		const encoders = tokens.map((token) => tokenToFunction(token, delimiter, encode));
		return (data, missing) => {
			let result = "";
			for (const encoder of encoders) result += encoder(data, missing);
			return result;
		};
	}
	/**
	* Convert a single token into a path building function.
	*/
	function tokenToFunction(token, delimiter, encode) {
		if (token.type === "text") return () => token.value;
		if (token.type === "group") {
			const fn = tokensToFunction(token.tokens, delimiter, encode);
			return (data, missing) => {
				const len = missing.length;
				const value = fn(data, missing);
				if (missing.length === len) return value;
				missing.length = len;
				return "";
			};
		}
		const encodeValue = encode || NOOP_VALUE;
		if (token.type === "wildcard" && encode !== false) return (data, missing) => {
			const value = data[token.name];
			if (value == null) {
				missing.push(token.name);
				return "";
			}
			if (!Array.isArray(value) || value.length === 0) throw new TypeError(`Expected "${token.name}" to be a non-empty array`);
			let result = "";
			for (let i = 0; i < value.length; i++) {
				if (typeof value[i] !== "string") throw new TypeError(`Expected "${token.name}/${i}" to be a string`);
				if (i > 0) result += delimiter;
				result += encodeValue(value[i]);
			}
			return result;
		};
		return (data, missing) => {
			const value = data[token.name];
			if (value == null) {
				missing.push(token.name);
				return "";
			}
			if (typeof value !== "string") throw new TypeError(`Expected "${token.name}" to be a string`);
			return encodeValue(value);
		};
	}
	/**
	* Transform a path into a match function.
	*/
	function match(path, options = {}) {
		const { decode = decodeURIComponent, delimiter = DEFAULT_DELIMITER } = options;
		const { regexp, keys } = pathToRegexp(path, options);
		const decoders = keys.map((key) => {
			if (decode === false) return NOOP_VALUE;
			if (key.type === "param") return decode;
			return (value) => value.split(delimiter).map(decode);
		});
		return function match(input) {
			const m = regexp.exec(input);
			if (!m) return false;
			const path = m[0];
			const params = Object.create(null);
			for (let i = 1; i < m.length; i++) {
				if (m[i] === void 0) continue;
				const key = keys[i - 1];
				const decoder = decoders[i - 1];
				params[key.name] = decoder(m[i]);
			}
			return {
				path,
				params
			};
		};
	}
	/**
	* Transform a path into a regular expression and capture keys.
	*/
	function pathToRegexp(path, options = {}) {
		const { delimiter = DEFAULT_DELIMITER, end = true, sensitive = false, trailing = true } = options;
		const keys = [];
		let source = "";
		let combinations = 0;
		function process(path) {
			if (Array.isArray(path)) {
				for (const p of path) process(p);
				return;
			}
			const data = typeof path === "object" ? path : parse(path, options);
			flatten(data.tokens, 0, [], (tokens) => {
				if (combinations >= 256) throw new PathError("Too many path combinations", data.originalPath);
				if (combinations > 0) source += "|";
				source += toRegExpSource(tokens, delimiter, keys, data.originalPath);
				combinations++;
			});
		}
		process(path);
		let pattern = `^(?:${source})`;
		if (trailing) pattern += "(?:" + escape(delimiter) + "$)?";
		pattern += end ? "$" : "(?=" + escape(delimiter) + "|$)";
		return {
			regexp: new RegExp(pattern, sensitive ? "" : "i"),
			keys
		};
	}
	/**
	* Generate a flat list of sequence tokens from the given tokens.
	*/
	function flatten(tokens, index, result, callback) {
		while (index < tokens.length) {
			const token = tokens[index++];
			if (token.type === "group") {
				const len = result.length;
				flatten(token.tokens, 0, result, (seq) => flatten(tokens, index, seq, callback));
				result.length = len;
				continue;
			}
			result.push(token);
		}
		callback(result);
	}
	/**
	* Transform a flat sequence of tokens into a regular expression.
	*/
	function toRegExpSource(tokens, delimiter, keys, originalPath) {
		let result = "";
		let backtrack = "";
		let wildcardBacktrack = "";
		let prevCaptureType = 0;
		let hasSegmentCapture = 0;
		let index = 0;
		function hasInSegment(index, type) {
			while (index < tokens.length) {
				const token = tokens[index++];
				if (token.type === type) return true;
				if (token.type === "text") {
					if (token.value.includes(delimiter)) break;
				}
			}
			return false;
		}
		function peekText(index) {
			let result = "";
			while (index < tokens.length) {
				const token = tokens[index++];
				if (token.type !== "text") break;
				result += token.value;
			}
			return result;
		}
		while (index < tokens.length) {
			const token = tokens[index++];
			if (token.type === "text") {
				result += escape(token.value);
				backtrack += token.value;
				if (prevCaptureType === 2) wildcardBacktrack += token.value;
				if (token.value.includes(delimiter)) hasSegmentCapture = 0;
				continue;
			}
			if (token.type === "param" || token.type === "wildcard") {
				if (prevCaptureType && !backtrack) throw new PathError(`Missing text before "${token.name}" ${token.type}`, originalPath);
				if (token.type === "param") {
					result += hasSegmentCapture & 2 ? `(${negate(delimiter, backtrack)}+)` : hasInSegment(index, "wildcard") ? `(${negate(delimiter, peekText(index))}+)` : hasSegmentCapture & 1 ? `(${negate(delimiter, backtrack)}+|${escape(backtrack)})` : `(${negate(delimiter, "")}+)`;
					hasSegmentCapture |= prevCaptureType = 1;
				} else {
					result += hasSegmentCapture & 2 ? `(${negate(backtrack, "")}+)` : wildcardBacktrack ? `(${negate(wildcardBacktrack, "")}+|${negate(delimiter, "")}+)` : `([^]+)`;
					wildcardBacktrack = "";
					hasSegmentCapture |= prevCaptureType = 2;
				}
				keys.push(token);
				backtrack = "";
				continue;
			}
			throw new TypeError(`Unknown token type: ${token.type}`);
		}
		return result;
	}
	/**
	* Block backtracking on previous text/delimiter.
	*/
	function negate(a, b) {
		if (b.length > a.length) return negate(b, a);
		if (a === b) b = "";
		if (b.length > 1) return `(?:(?!${escape(a)}|${escape(b)})[^])`;
		if (a.length > 1) return `(?:(?!${escape(a)})[^${escape(b)}])`;
		return `[^${escape(a + b)}]`;
	}
	/**
	* Stringify an array of tokens into a path string.
	*/
	function stringifyTokens(tokens, index) {
		let value = "";
		while (index < tokens.length) {
			const token = tokens[index++];
			if (token.type === "text") {
				value += escapeText(token.value);
				continue;
			}
			if (token.type === "group") {
				value += "{" + stringifyTokens(token.tokens, 0) + "}";
				continue;
			}
			if (token.type === "param") {
				value += ":" + stringifyName(token.name, tokens[index]);
				continue;
			}
			if (token.type === "wildcard") {
				value += "*" + stringifyName(token.name, tokens[index]);
				continue;
			}
			throw new TypeError(`Unknown token type: ${token.type}`);
		}
		return value;
	}
	/**
	* Stringify token data into a path string.
	*/
	function stringify(data) {
		return stringifyTokens(data.tokens, 0);
	}
	/**
	* Stringify a parameter name, escaping when it cannot be emitted directly.
	*/
	function stringifyName(name, next) {
		if (!ID.test(name)) return JSON.stringify(name);
		if ((next === null || next === void 0 ? void 0 : next.type) === "text" && ID_CONTINUE.test(next.value[0])) return JSON.stringify(name);
		return name;
	}
}));
//#endregion
//#region ../node_modules/router/lib/layer.js
/*!
* router
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2022 Douglas Christopher Wilson
* MIT Licensed
*/
var require_layer = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	const isPromise = require_is_promise();
	const pathRegexp = require_dist();
	const debug = require_src()("router:layer");
	const deprecate = require_depd()("router");
	/**
	* Module variables.
	* @private
	*/
	const TRAILING_SLASH_REGEXP = /\/+$/;
	const MATCHING_GROUP_REGEXP = /\((?:\?<(.*?)>)?(?!\?)/g;
	/**
	* Expose `Layer`.
	*/
	module.exports = Layer;
	function Layer(path, options, fn) {
		if (!(this instanceof Layer)) return new Layer(path, options, fn);
		debug("new %o", path);
		const opts = options || {};
		this.handle = fn;
		this.keys = [];
		this.name = fn.name || "<anonymous>";
		this.params = void 0;
		this.path = void 0;
		this.slash = path === "/" && opts.end === false;
		function matcher(_path) {
			if (_path instanceof RegExp) {
				const keys = [];
				let name = 0;
				let m;
				while (m = MATCHING_GROUP_REGEXP.exec(_path.source)) keys.push({
					name: m[1] || name++,
					offset: m.index
				});
				return function regexpMatcher(p) {
					const match = _path.exec(p);
					if (!match) return false;
					const params = {};
					for (let i = 1; i < match.length; i++) {
						const prop = keys[i - 1].name;
						const val = decodeParam(match[i]);
						if (val !== void 0) params[prop] = val;
					}
					return {
						params,
						path: match[0]
					};
				};
			}
			return pathRegexp.match(opts.strict ? _path : loosen(_path), {
				sensitive: opts.sensitive,
				end: opts.end,
				trailing: !opts.strict,
				decode: decodeParam
			});
		}
		this.matchers = Array.isArray(path) ? path.map(matcher) : [matcher(path)];
	}
	/**
	* Handle the error for the layer.
	*
	* @param {Error} error
	* @param {Request} req
	* @param {Response} res
	* @param {function} next
	* @api private
	*/
	Layer.prototype.handleError = function handleError(error, req, res, next) {
		const fn = this.handle;
		if (fn.length !== 4) return next(error);
		try {
			const ret = fn(error, req, res, next);
			if (isPromise(ret)) {
				if (!(ret instanceof Promise)) deprecate("handlers that are Promise-like are deprecated, use a native Promise instead");
				ret.then(null, function(error) {
					next(error || /* @__PURE__ */ new Error("Rejected promise"));
				});
			}
		} catch (err) {
			next(err);
		}
	};
	/**
	* Handle the request for the layer.
	*
	* @param {Request} req
	* @param {Response} res
	* @param {function} next
	* @api private
	*/
	Layer.prototype.handleRequest = function handleRequest(req, res, next) {
		const fn = this.handle;
		if (fn.length > 3) return next();
		try {
			const ret = fn(req, res, next);
			if (isPromise(ret)) {
				if (!(ret instanceof Promise)) deprecate("handlers that are Promise-like are deprecated, use a native Promise instead");
				ret.then(null, function(error) {
					next(error || /* @__PURE__ */ new Error("Rejected promise"));
				});
			}
		} catch (err) {
			next(err);
		}
	};
	/**
	* Check if this route matches `path`, if so
	* populate `.params`.
	*
	* @param {String} path
	* @return {Boolean}
	* @api private
	*/
	Layer.prototype.match = function match(path) {
		let match;
		if (path != null) {
			if (this.slash) {
				this.params = {};
				this.path = "";
				return true;
			}
			let i = 0;
			while (!match && i < this.matchers.length) {
				match = this.matchers[i](path);
				i++;
			}
		}
		if (!match) {
			this.params = void 0;
			this.path = void 0;
			return false;
		}
		this.params = match.params;
		this.path = match.path;
		this.keys = Object.keys(match.params);
		return true;
	};
	/**
	* Decode param value.
	*
	* @param {string} val
	* @return {string}
	* @private
	*/
	function decodeParam(val) {
		if (typeof val !== "string" || val.length === 0) return val;
		try {
			return decodeURIComponent(val);
		} catch (err) {
			if (err instanceof URIError) {
				err.message = "Failed to decode param '" + val + "'";
				err.status = 400;
			}
			throw err;
		}
	}
	/**
	* Loosens the given path for path-to-regexp matching.
	*/
	function loosen(path) {
		if (path instanceof RegExp || path === "/") return path;
		return Array.isArray(path) ? path.map(function(p) {
			return loosen(p);
		}) : String(path).replace(TRAILING_SLASH_REGEXP, "");
	}
}));
//#endregion
//#region ../node_modules/router/lib/route.js
/*!
* router
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2022 Douglas Christopher Wilson
* MIT Licensed
*/
var require_route = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	const debug = require_src()("router:route");
	const Layer = require_layer();
	const { METHODS: METHODS$1 } = __require("node:http");
	/**
	* Module variables.
	* @private
	*/
	const slice = Array.prototype.slice;
	const flatten = Array.prototype.flat;
	const methods = METHODS$1.map((method) => method.toLowerCase());
	/**
	* Expose `Route`.
	*/
	module.exports = Route;
	/**
	* Initialize `Route` with the given `path`,
	*
	* @param {String} path
	* @api private
	*/
	function Route(path) {
		debug("new %o", path);
		this.path = path;
		this.stack = [];
		this.methods = Object.create(null);
	}
	/**
	* @private
	*/
	Route.prototype._handlesMethod = function _handlesMethod(method) {
		if (this.methods._all) return true;
		let name = typeof method === "string" ? method.toLowerCase() : method;
		if (name === "head" && !this.methods.head) name = "get";
		return Boolean(this.methods[name]);
	};
	/**
	* @return {array} supported HTTP methods
	* @private
	*/
	Route.prototype._methods = function _methods() {
		const methods = Object.keys(this.methods);
		if (this.methods.get && !this.methods.head) methods.push("head");
		for (let i = 0; i < methods.length; i++) methods[i] = methods[i].toUpperCase();
		return methods;
	};
	/**
	* dispatch req, res into this route
	*
	* @private
	*/
	Route.prototype.dispatch = function dispatch(req, res, done) {
		let idx = 0;
		const stack = this.stack;
		let sync = 0;
		if (stack.length === 0) return done();
		let method = typeof req.method === "string" ? req.method.toLowerCase() : req.method;
		if (method === "head" && !this.methods.head) method = "get";
		req.route = this;
		next();
		function next(err) {
			if (err && err === "route") return done();
			if (err && err === "router") return done(err);
			if (idx >= stack.length) return done(err);
			if (++sync > 100) return setImmediate(next, err);
			let layer;
			let match;
			while (match !== true && idx < stack.length) {
				layer = stack[idx++];
				match = !layer.method || layer.method === method;
			}
			if (match !== true) return done(err);
			if (err) layer.handleError(err, req, res, next);
			else layer.handleRequest(req, res, next);
			sync = 0;
		}
	};
	/**
	* Add a handler for all HTTP verbs to this route.
	*
	* Behaves just like middleware and can respond or call `next`
	* to continue processing.
	*
	* You can use multiple `.all` call to add multiple handlers.
	*
	*   function check_something(req, res, next){
	*     next()
	*   }
	*
	*   function validate_user(req, res, next){
	*     next()
	*   }
	*
	*   route
	*   .all(validate_user)
	*   .all(check_something)
	*   .get(function(req, res, next){
	*     res.send('hello world')
	*   })
	*
	* @param {array|function} handler
	* @return {Route} for chaining
	* @api public
	*/
	Route.prototype.all = function all(handler) {
		const callbacks = flatten.call(slice.call(arguments), Infinity);
		if (callbacks.length === 0) throw new TypeError("argument handler is required");
		for (let i = 0; i < callbacks.length; i++) {
			const fn = callbacks[i];
			if (typeof fn !== "function") throw new TypeError("argument handler must be a function");
			const layer = Layer("/", {}, fn);
			layer.method = void 0;
			this.methods._all = true;
			this.stack.push(layer);
		}
		return this;
	};
	methods.forEach(function(method) {
		Route.prototype[method] = function(handler) {
			const callbacks = flatten.call(slice.call(arguments), Infinity);
			if (callbacks.length === 0) throw new TypeError("argument handler is required");
			for (let i = 0; i < callbacks.length; i++) {
				const fn = callbacks[i];
				if (typeof fn !== "function") throw new TypeError("argument handler must be a function");
				debug("%s %s", method, this.path);
				const layer = Layer("/", {}, fn);
				layer.method = method;
				this.methods[method] = true;
				this.stack.push(layer);
			}
			return this;
		};
	});
}));
//#endregion
//#region ../node_modules/router/index.js
/*!
* router
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2022 Douglas Christopher Wilson
* MIT Licensed
*/
var require_router = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	const isPromise = require_is_promise();
	const Layer = require_layer();
	const { METHODS } = __require("node:http");
	const parseUrl = require_parseurl();
	const Route = require_route();
	const debug = require_src()("router");
	const deprecate = require_depd()("router");
	/**
	* Module variables.
	* @private
	*/
	const slice = Array.prototype.slice;
	const flatten = Array.prototype.flat;
	const methods = METHODS.map((method) => method.toLowerCase());
	/**
	* Expose `Router`.
	*/
	module.exports = Router;
	/**
	* Expose `Route`.
	*/
	module.exports.Route = Route;
	/**
	* Initialize a new `Router` with the given `options`.
	*
	* @param {object} [options]
	* @return {Router} which is a callable function
	* @public
	*/
	function Router(options) {
		if (!(this instanceof Router)) return new Router(options);
		const opts = options || {};
		function router(req, res, next) {
			router.handle(req, res, next);
		}
		Object.setPrototypeOf(router, this);
		router.caseSensitive = opts.caseSensitive;
		router.mergeParams = opts.mergeParams;
		router.params = {};
		router.strict = opts.strict;
		router.stack = [];
		return router;
	}
	/**
	* Router prototype inherits from a Function.
	*/
	/* istanbul ignore next */
	Router.prototype = function() {};
	/**
	* Map the given param placeholder `name`(s) to the given callback.
	*
	* Parameter mapping is used to provide pre-conditions to routes
	* which use normalized placeholders. For example a _:user_id_ parameter
	* could automatically load a user's information from the database without
	* any additional code.
	*
	* The callback uses the same signature as middleware, the only difference
	* being that the value of the placeholder is passed, in this case the _id_
	* of the user. Once the `next()` function is invoked, just like middleware
	* it will continue on to execute the route, or subsequent parameter functions.
	*
	* Just like in middleware, you must either respond to the request or call next
	* to avoid stalling the request.
	*
	*  router.param('user_id', function(req, res, next, id){
	*    User.find(id, function(err, user){
	*      if (err) {
	*        return next(err)
	*      } else if (!user) {
	*        return next(new Error('failed to load user'))
	*      }
	*      req.user = user
	*      next()
	*    })
	*  })
	*
	* @param {string} name
	* @param {function} fn
	* @public
	*/
	Router.prototype.param = function param(name, fn) {
		if (!name) throw new TypeError("argument name is required");
		if (typeof name !== "string") throw new TypeError("argument name must be a string");
		if (!fn) throw new TypeError("argument fn is required");
		if (typeof fn !== "function") throw new TypeError("argument fn must be a function");
		let params = this.params[name];
		if (!params) params = this.params[name] = [];
		params.push(fn);
		return this;
	};
	/**
	* Dispatch a req, res into the router.
	*
	* @private
	*/
	Router.prototype.handle = function handle(req, res, callback) {
		if (!callback) throw new TypeError("argument callback is required");
		debug("dispatching %s %s", req.method, req.url);
		let idx = 0;
		let methods;
		const protohost = getProtohost(req.url) || "";
		let removed = "";
		const self = this;
		let slashAdded = false;
		let sync = 0;
		const paramcalled = {};
		const stack = this.stack;
		const parentParams = req.params;
		const parentUrl = req.baseUrl || "";
		let done = restore(callback, req, "baseUrl", "next", "params");
		req.next = next;
		if (req.method === "OPTIONS") {
			methods = [];
			done = wrap(done, generateOptionsResponder(res, methods));
		}
		req.baseUrl = parentUrl;
		req.originalUrl = req.originalUrl || req.url;
		next();
		function next(err) {
			let layerError = err === "route" ? null : err;
			if (slashAdded) {
				req.url = req.url.slice(1);
				slashAdded = false;
			}
			if (removed.length !== 0) {
				req.baseUrl = parentUrl;
				req.url = protohost + removed + req.url.slice(protohost.length);
				removed = "";
			}
			if (layerError === "router") {
				setImmediate(done, null);
				return;
			}
			if (idx >= stack.length) {
				setImmediate(done, layerError);
				return;
			}
			if (++sync > 100) return setImmediate(next, err);
			const path = getPathname(req);
			if (path == null) return done(layerError);
			let layer;
			let match;
			let route;
			while (match !== true && idx < stack.length) {
				layer = stack[idx++];
				match = matchLayer(layer, path);
				route = layer.route;
				if (typeof match !== "boolean") layerError = layerError || match;
				if (match !== true) continue;
				if (!route) continue;
				if (layerError) {
					match = false;
					continue;
				}
				const method = req.method;
				const hasMethod = route._handlesMethod(method);
				if (!hasMethod && method === "OPTIONS" && methods) methods.push.apply(methods, route._methods());
				if (!hasMethod && method !== "HEAD") match = false;
			}
			if (match !== true) return done(layerError);
			if (route) req.route = route;
			req.params = self.mergeParams ? mergeParams(layer.params, parentParams) : layer.params;
			const layerPath = layer.path;
			processParams(self.params, layer, paramcalled, req, res, function(err) {
				if (err) next(layerError || err);
				else if (route) layer.handleRequest(req, res, next);
				else trimPrefix(layer, layerError, layerPath, path);
				sync = 0;
			});
		}
		function trimPrefix(layer, layerError, layerPath, path) {
			if (layerPath.length !== 0) {
				if (layerPath !== path.substring(0, layerPath.length)) {
					next(layerError);
					return;
				}
				const c = path[layerPath.length];
				if (c && c !== "/") {
					next(layerError);
					return;
				}
				debug("trim prefix (%s) from url %s", layerPath, req.url);
				removed = layerPath;
				req.url = protohost + req.url.slice(protohost.length + removed.length);
				if (!protohost && req.url[0] !== "/") {
					req.url = "/" + req.url;
					slashAdded = true;
				}
				req.baseUrl = parentUrl + (removed[removed.length - 1] === "/" ? removed.substring(0, removed.length - 1) : removed);
			}
			debug("%s %s : %s", layer.name, layerPath, req.originalUrl);
			if (layerError) layer.handleError(layerError, req, res, next);
			else layer.handleRequest(req, res, next);
		}
	};
	/**
	* Use the given middleware function, with optional path, defaulting to "/".
	*
	* Use (like `.all`) will run for any http METHOD, but it will not add
	* handlers for those methods so OPTIONS requests will not consider `.use`
	* functions even if they could respond.
	*
	* The other difference is that _route_ path is stripped and not visible
	* to the handler function. The main effect of this feature is that mounted
	* handlers can operate without any code changes regardless of the "prefix"
	* pathname.
	*
	* @public
	*/
	Router.prototype.use = function use(handler) {
		let offset = 0;
		let path = "/";
		if (typeof handler !== "function") {
			let arg = handler;
			while (Array.isArray(arg) && arg.length !== 0) arg = arg[0];
			if (typeof arg !== "function") {
				offset = 1;
				path = handler;
			}
		}
		const callbacks = flatten.call(slice.call(arguments, offset), Infinity);
		if (callbacks.length === 0) throw new TypeError("argument handler is required");
		for (let i = 0; i < callbacks.length; i++) {
			const fn = callbacks[i];
			if (typeof fn !== "function") throw new TypeError("argument handler must be a function");
			debug("use %o %s", path, fn.name || "<anonymous>");
			const layer = new Layer(path, {
				sensitive: this.caseSensitive,
				strict: false,
				end: false
			}, fn);
			layer.route = void 0;
			this.stack.push(layer);
		}
		return this;
	};
	/**
	* Create a new Route for the given path.
	*
	* Each route contains a separate middleware stack and VERB handlers.
	*
	* See the Route api documentation for details on adding handlers
	* and middleware to routes.
	*
	* @param {string} path
	* @return {Route}
	* @public
	*/
	Router.prototype.route = function route(path) {
		const route = new Route(path);
		const layer = new Layer(path, {
			sensitive: this.caseSensitive,
			strict: this.strict,
			end: true
		}, handle);
		function handle(req, res, next) {
			route.dispatch(req, res, next);
		}
		layer.route = route;
		this.stack.push(layer);
		return route;
	};
	methods.concat("all").forEach(function(method) {
		Router.prototype[method] = function(path) {
			const route = this.route(path);
			route[method].apply(route, slice.call(arguments, 1));
			return this;
		};
	});
	/**
	* Generate a callback that will make an OPTIONS response.
	*
	* @param {OutgoingMessage} res
	* @param {array} methods
	* @private
	*/
	function generateOptionsResponder(res, methods) {
		return function onDone(fn, err) {
			if (err || methods.length === 0) return fn(err);
			trySendOptionsResponse(res, methods, fn);
		};
	}
	/**
	* Get pathname of request.
	*
	* @param {IncomingMessage} req
	* @private
	*/
	function getPathname(req) {
		try {
			return parseUrl(req).pathname;
		} catch (err) {
			return;
		}
	}
	/**
	* Get get protocol + host for a URL.
	*
	* @param {string} url
	* @private
	*/
	function getProtohost(url) {
		if (typeof url !== "string" || url.length === 0 || url[0] === "/") return;
		const searchIndex = url.indexOf("?");
		const pathLength = searchIndex !== -1 ? searchIndex : url.length;
		const fqdnIndex = url.substring(0, pathLength).indexOf("://");
		return fqdnIndex !== -1 ? url.substring(0, url.indexOf("/", 3 + fqdnIndex)) : void 0;
	}
	/**
	* Match path to a layer.
	*
	* @param {Layer} layer
	* @param {string} path
	* @private
	*/
	function matchLayer(layer, path) {
		try {
			return layer.match(path);
		} catch (err) {
			return err;
		}
	}
	/**
	* Merge params with parent params
	*
	* @private
	*/
	function mergeParams(params, parent) {
		if (typeof parent !== "object" || !parent) return params;
		const obj = Object.assign({}, parent);
		if (!(0 in params) || !(0 in parent)) return Object.assign(obj, params);
		let i = 0;
		let o = 0;
		while (i in params) i++;
		while (o in parent) o++;
		for (i--; i >= 0; i--) {
			params[i + o] = params[i];
			if (i < o) delete params[i];
		}
		return Object.assign(obj, params);
	}
	/**
	* Process any parameters for the layer.
	*
	* @private
	*/
	function processParams(params, layer, called, req, res, done) {
		const keys = layer.keys;
		if (!keys || keys.length === 0) return done();
		let i = 0;
		let paramIndex = 0;
		let key;
		let paramVal;
		let paramCallbacks;
		let paramCalled;
		function param(err) {
			if (err) return done(err);
			if (i >= keys.length) return done();
			paramIndex = 0;
			key = keys[i++];
			paramVal = req.params[key];
			paramCallbacks = params[key];
			paramCalled = called[key];
			if (paramVal === void 0 || !paramCallbacks) return param();
			if (paramCalled && (paramCalled.match === paramVal || paramCalled.error && paramCalled.error !== "route")) {
				req.params[key] = paramCalled.value;
				return param(paramCalled.error);
			}
			called[key] = paramCalled = {
				error: null,
				match: paramVal,
				value: paramVal
			};
			paramCallback();
		}
		function paramCallback(err) {
			const fn = paramCallbacks[paramIndex++];
			paramCalled.value = req.params[key];
			if (err) {
				paramCalled.error = err;
				param(err);
				return;
			}
			if (!fn) return param();
			try {
				const ret = fn(req, res, paramCallback, paramVal, key);
				if (isPromise(ret)) {
					if (!(ret instanceof Promise)) deprecate("parameters that are Promise-like are deprecated, use a native Promise instead");
					ret.then(null, function(error) {
						paramCallback(error || /* @__PURE__ */ new Error("Rejected promise"));
					});
				}
			} catch (e) {
				paramCallback(e);
			}
		}
		param();
	}
	/**
	* Restore obj props after function
	*
	* @private
	*/
	function restore(fn, obj) {
		const props = new Array(arguments.length - 2);
		const vals = new Array(arguments.length - 2);
		for (let i = 0; i < props.length; i++) {
			props[i] = arguments[i + 2];
			vals[i] = obj[props[i]];
		}
		return function() {
			for (let i = 0; i < props.length; i++) obj[props[i]] = vals[i];
			return fn.apply(this, arguments);
		};
	}
	/**
	* Send an OPTIONS response.
	*
	* @private
	*/
	function sendOptionsResponse(res, methods) {
		const options = Object.create(null);
		for (let i = 0; i < methods.length; i++) options[methods[i]] = true;
		const allow = Object.keys(options).sort().join(", ");
		res.setHeader("Allow", allow);
		res.setHeader("Content-Length", Buffer.byteLength(allow));
		res.setHeader("Content-Type", "text/plain");
		res.setHeader("X-Content-Type-Options", "nosniff");
		res.end(allow);
	}
	/**
	* Try to send an OPTIONS response.
	*
	* @private
	*/
	function trySendOptionsResponse(res, methods, next) {
		try {
			sendOptionsResponse(res, methods);
		} catch (err) {
			next(err);
		}
	}
	/**
	* Wrap a function
	*
	* @private
	*/
	function wrap(old, fn) {
		return function proxy() {
			const args = new Array(arguments.length + 1);
			args[0] = old;
			for (let i = 0, len = arguments.length; i < len; i++) args[i + 1] = arguments[i];
			fn.apply(this, args);
		};
	}
}));
//#endregion
//#region ../node_modules/express/lib/application.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_application = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var finalhandler = require_finalhandler();
	var debug = require_src()("express:application");
	var View = require_view();
	var http$2 = __require("node:http");
	var methods = require_utils().methods;
	var compileETag = require_utils().compileETag;
	var compileQueryParser = require_utils().compileQueryParser;
	var compileTrust = require_utils().compileTrust;
	var resolve$1 = __require("node:path").resolve;
	var once = require_once();
	var Router = require_router();
	/**
	* Module variables.
	* @private
	*/
	var slice = Array.prototype.slice;
	var flatten = Array.prototype.flat;
	/**
	* Application prototype.
	*/
	var app = exports = module.exports = {};
	/**
	* Variable for trust proxy inheritance back-compat
	* @private
	*/
	var trustProxyDefaultSymbol = "@@symbol:trust_proxy_default";
	/**
	* Initialize the server.
	*
	*   - setup default configuration
	*   - setup default middleware
	*   - setup route reflection methods
	*
	* @private
	*/
	app.init = function init() {
		var router = null;
		this.cache = Object.create(null);
		this.engines = Object.create(null);
		this.settings = Object.create(null);
		this.defaultConfiguration();
		Object.defineProperty(this, "router", {
			configurable: true,
			enumerable: true,
			get: function getrouter() {
				if (router === null) router = new Router({
					caseSensitive: this.enabled("case sensitive routing"),
					strict: this.enabled("strict routing")
				});
				return router;
			}
		});
	};
	/**
	* Initialize application configuration.
	* @private
	*/
	app.defaultConfiguration = function defaultConfiguration() {
		var env = process.env.NODE_ENV || "development";
		this.enable("x-powered-by");
		this.set("etag", "weak");
		this.set("env", env);
		this.set("query parser", "simple");
		this.set("subdomain offset", 2);
		this.set("trust proxy", false);
		Object.defineProperty(this.settings, trustProxyDefaultSymbol, {
			configurable: true,
			value: true
		});
		debug("booting in %s mode", env);
		this.on("mount", function onmount(parent) {
			if (this.settings[trustProxyDefaultSymbol] === true && typeof parent.settings["trust proxy fn"] === "function") {
				delete this.settings["trust proxy"];
				delete this.settings["trust proxy fn"];
			}
			Object.setPrototypeOf(this.request, parent.request);
			Object.setPrototypeOf(this.response, parent.response);
			Object.setPrototypeOf(this.engines, parent.engines);
			Object.setPrototypeOf(this.settings, parent.settings);
		});
		this.locals = Object.create(null);
		this.mountpath = "/";
		this.locals.settings = this.settings;
		this.set("view", View);
		this.set("views", resolve$1("views"));
		this.set("jsonp callback name", "callback");
		if (env === "production") this.enable("view cache");
	};
	/**
	* Dispatch a req, res pair into the application. Starts pipeline processing.
	*
	* If no callback is provided, then default error handlers will respond
	* in the event of an error bubbling through the stack.
	*
	* @private
	*/
	app.handle = function handle(req, res, callback) {
		var done = callback || finalhandler(req, res, {
			env: this.get("env"),
			onerror: logerror.bind(this)
		});
		if (this.enabled("x-powered-by")) res.setHeader("X-Powered-By", "Express");
		req.res = res;
		res.req = req;
		Object.setPrototypeOf(req, this.request);
		Object.setPrototypeOf(res, this.response);
		if (!res.locals) res.locals = Object.create(null);
		this.router.handle(req, res, done);
	};
	/**
	* Proxy `Router#use()` to add middleware to the app router.
	* See Router#use() documentation for details.
	*
	* If the _fn_ parameter is an express app, then it will be
	* mounted at the _route_ specified.
	*
	* @public
	*/
	app.use = function use(fn) {
		var offset = 0;
		var path = "/";
		if (typeof fn !== "function") {
			var arg = fn;
			while (Array.isArray(arg) && arg.length !== 0) arg = arg[0];
			if (typeof arg !== "function") {
				offset = 1;
				path = fn;
			}
		}
		var fns = flatten.call(slice.call(arguments, offset), Infinity);
		if (fns.length === 0) throw new TypeError("app.use() requires a middleware function");
		var router = this.router;
		fns.forEach(function(fn) {
			if (!fn || !fn.handle || !fn.set) return router.use(path, fn);
			debug(".use app under %s", path);
			fn.mountpath = path;
			fn.parent = this;
			router.use(path, function mounted_app(req, res, next) {
				var orig = req.app;
				fn.handle(req, res, function(err) {
					Object.setPrototypeOf(req, orig.request);
					Object.setPrototypeOf(res, orig.response);
					next(err);
				});
			});
			fn.emit("mount", this);
		}, this);
		return this;
	};
	/**
	* Proxy to the app `Router#route()`
	* Returns a new `Route` instance for the _path_.
	*
	* Routes are isolated middleware stacks for specific paths.
	* See the Route api docs for details.
	*
	* @public
	*/
	app.route = function route(path) {
		return this.router.route(path);
	};
	/**
	* Register the given template engine callback `fn`
	* as `ext`.
	*
	* By default will `require()` the engine based on the
	* file extension. For example if you try to render
	* a "foo.ejs" file Express will invoke the following internally:
	*
	*     app.engine('ejs', require('ejs').__express);
	*
	* For engines that do not provide `.__express` out of the box,
	* or if you wish to "map" a different extension to the template engine
	* you may use this method. For example mapping the EJS template engine to
	* ".html" files:
	*
	*     app.engine('html', require('ejs').renderFile);
	*
	* In this case EJS provides a `.renderFile()` method with
	* the same signature that Express expects: `(path, options, callback)`,
	* though note that it aliases this method as `ejs.__express` internally
	* so if you're using ".ejs" extensions you don't need to do anything.
	*
	* Some template engines do not follow this convention, the
	* [Consolidate.js](https://github.com/tj/consolidate.js)
	* library was created to map all of node's popular template
	* engines to follow this convention, thus allowing them to
	* work seamlessly within Express.
	*
	* @param {String} ext
	* @param {Function} fn
	* @return {app} for chaining
	* @public
	*/
	app.engine = function engine(ext, fn) {
		if (typeof fn !== "function") throw new Error("callback function required");
		var extension = ext[0] !== "." ? "." + ext : ext;
		this.engines[extension] = fn;
		return this;
	};
	/**
	* Proxy to `Router#param()` with one added api feature. The _name_ parameter
	* can be an array of names.
	*
	* See the Router#param() docs for more details.
	*
	* @param {String|Array} name
	* @param {Function} fn
	* @return {app} for chaining
	* @public
	*/
	app.param = function param(name, fn) {
		if (Array.isArray(name)) {
			for (var i = 0; i < name.length; i++) this.param(name[i], fn);
			return this;
		}
		this.router.param(name, fn);
		return this;
	};
	/**
	* Assign `setting` to `val`, or return `setting`'s value.
	*
	*    app.set('foo', 'bar');
	*    app.set('foo');
	*    // => "bar"
	*
	* Mounted servers inherit their parent server's settings.
	*
	* @param {String} setting
	* @param {*} [val]
	* @return {Server} for chaining
	* @public
	*/
	app.set = function set(setting, val) {
		if (arguments.length === 1) return this.settings[setting];
		debug("set \"%s\" to %o", setting, val);
		this.settings[setting] = val;
		switch (setting) {
			case "etag":
				this.set("etag fn", compileETag(val));
				break;
			case "query parser":
				this.set("query parser fn", compileQueryParser(val));
				break;
			case "trust proxy":
				this.set("trust proxy fn", compileTrust(val));
				Object.defineProperty(this.settings, trustProxyDefaultSymbol, {
					configurable: true,
					value: false
				});
		}
		return this;
	};
	/**
	* Return the app's absolute pathname
	* based on the parent(s) that have
	* mounted it.
	*
	* For example if the application was
	* mounted as "/admin", which itself
	* was mounted as "/blog" then the
	* return value would be "/blog/admin".
	*
	* @return {String}
	* @private
	*/
	app.path = function path() {
		return this.parent ? this.parent.path() + this.mountpath : "";
	};
	/**
	* Check if `setting` is enabled (truthy).
	*
	*    app.enabled('foo')
	*    // => false
	*
	*    app.enable('foo')
	*    app.enabled('foo')
	*    // => true
	*
	* @param {String} setting
	* @return {Boolean}
	* @public
	*/
	app.enabled = function enabled(setting) {
		return Boolean(this.set(setting));
	};
	/**
	* Check if `setting` is disabled.
	*
	*    app.disabled('foo')
	*    // => true
	*
	*    app.enable('foo')
	*    app.disabled('foo')
	*    // => false
	*
	* @param {String} setting
	* @return {Boolean}
	* @public
	*/
	app.disabled = function disabled(setting) {
		return !this.set(setting);
	};
	/**
	* Enable `setting`.
	*
	* @param {String} setting
	* @return {app} for chaining
	* @public
	*/
	app.enable = function enable(setting) {
		return this.set(setting, true);
	};
	/**
	* Disable `setting`.
	*
	* @param {String} setting
	* @return {app} for chaining
	* @public
	*/
	app.disable = function disable(setting) {
		return this.set(setting, false);
	};
	/**
	* Delegate `.VERB(...)` calls to `router.VERB(...)`.
	*/
	methods.forEach(function(method) {
		app[method] = function(path) {
			if (method === "get" && arguments.length === 1) return this.set(path);
			var route = this.route(path);
			route[method].apply(route, slice.call(arguments, 1));
			return this;
		};
	});
	/**
	* Special-cased "all" method, applying the given route `path`,
	* middleware, and callback to _every_ HTTP method.
	*
	* @param {String} path
	* @param {Function} ...
	* @return {app} for chaining
	* @public
	*/
	app.all = function all(path) {
		var route = this.route(path);
		var args = slice.call(arguments, 1);
		for (var i = 0; i < methods.length; i++) route[methods[i]].apply(route, args);
		return this;
	};
	/**
	* Render the given view `name` name with `options`
	* and a callback accepting an error and the
	* rendered template string.
	*
	* Example:
	*
	*    app.render('email', { name: 'Tobi' }, function(err, html){
	*      // ...
	*    })
	*
	* @param {String} name
	* @param {Object|Function} options or fn
	* @param {Function} callback
	* @public
	*/
	app.render = function render(name, options, callback) {
		var cache = this.cache;
		var done = callback;
		var engines = this.engines;
		var opts = options;
		var view;
		if (typeof options === "function") {
			done = options;
			opts = {};
		}
		var renderOptions = {
			...this.locals,
			...opts._locals,
			...opts
		};
		if (renderOptions.cache == null) renderOptions.cache = this.enabled("view cache");
		if (renderOptions.cache) view = cache[name];
		if (!view) {
			view = new (this.get("view"))(name, {
				defaultEngine: this.get("view engine"),
				root: this.get("views"),
				engines
			});
			if (!view.path) {
				var dirs = Array.isArray(view.root) && view.root.length > 1 ? "directories \"" + view.root.slice(0, -1).join("\", \"") + "\" or \"" + view.root[view.root.length - 1] + "\"" : "directory \"" + view.root + "\"";
				var err = /* @__PURE__ */ new Error("Failed to lookup view \"" + name + "\" in views " + dirs);
				err.view = view;
				return done(err);
			}
			if (renderOptions.cache) cache[name] = view;
		}
		tryRender(view, renderOptions, done);
	};
	/**
	* Listen for connections.
	*
	* A node `http.Server` is returned, with this
	* application (which is a `Function`) as its
	* callback. If you wish to create both an HTTP
	* and HTTPS server you may do so with the "http"
	* and "https" modules as shown here:
	*
	*    var http = require('node:http')
	*      , https = require('node:https')
	*      , express = require('express')
	*      , app = express();
	*
	*    http.createServer(app).listen(80);
	*    https.createServer({ ... }, app).listen(443);
	*
	* @return {http.Server}
	* @public
	*/
	app.listen = function listen() {
		var server = http$2.createServer(this);
		var args = slice.call(arguments);
		if (typeof args[args.length - 1] === "function") {
			var done = args[args.length - 1] = once(args[args.length - 1]);
			server.once("error", done);
		}
		return server.listen.apply(server, args);
	};
	/**
	* Log error using console.error.
	*
	* @param {Error} err
	* @private
	*/
	function logerror(err) {
		/* istanbul ignore next */
		if (this.get("env") !== "test") console.error(err.stack || err.toString());
	}
	/**
	* Try rendering a view.
	* @private
	*/
	function tryRender(view, options, callback) {
		try {
			view.render(options, callback);
		} catch (err) {
			callback(err);
		}
	}
}));
//#endregion
//#region ../node_modules/fresh/index.js
/*!
* fresh
* Copyright(c) 2012 TJ Holowaychuk
* Copyright(c) 2016-2017 Douglas Christopher Wilson
* MIT Licensed
*/
var require_fresh = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* RegExp to check for no-cache token in Cache-Control.
	* @private
	*/
	var CACHE_CONTROL_NO_CACHE_REGEXP = /(?:^|,)\s*?no-cache\s*?(?:,|$)/;
	/**
	* Module exports.
	* @public
	*/
	module.exports = fresh;
	/**
	* Check freshness of the response using request and response headers.
	*
	* @param {Object} reqHeaders
	* @param {Object} resHeaders
	* @return {Boolean}
	* @public
	*/
	function fresh(reqHeaders, resHeaders) {
		var modifiedSince = reqHeaders["if-modified-since"];
		var noneMatch = reqHeaders["if-none-match"];
		if (!modifiedSince && !noneMatch) return false;
		var cacheControl = reqHeaders["cache-control"];
		if (cacheControl && CACHE_CONTROL_NO_CACHE_REGEXP.test(cacheControl)) return false;
		if (noneMatch) {
			if (noneMatch === "*") return true;
			var etag = resHeaders.etag;
			if (!etag) return false;
			var matches = parseTokenList(noneMatch);
			for (var i = 0; i < matches.length; i++) {
				var match = matches[i];
				if (match === etag || match === "W/" + etag || "W/" + match === etag) return true;
			}
			return false;
		}
		if (modifiedSince) {
			var lastModified = resHeaders["last-modified"];
			if (!lastModified || !(parseHttpDate(lastModified) <= parseHttpDate(modifiedSince))) return false;
		}
		return true;
	}
	/**
	* Parse an HTTP Date into a number.
	*
	* @param {string} date
	* @private
	*/
	function parseHttpDate(date) {
		var timestamp = date && Date.parse(date);
		// istanbul ignore next: guard against date.js Date.parse patching
		return typeof timestamp === "number" ? timestamp : NaN;
	}
	/**
	* Parse a HTTP token list.
	*
	* @param {string} str
	* @private
	*/
	function parseTokenList(str) {
		var end = 0;
		var list = [];
		var start = 0;
		for (var i = 0, len = str.length; i < len; i++) switch (str.charCodeAt(i)) {
			case 32:
				if (start === end) start = end = i + 1;
				break;
			case 44:
				list.push(str.substring(start, end));
				start = end = i + 1;
				break;
			default: end = i + 1;
		}
		list.push(str.substring(start, end));
		return list;
	}
}));
//#endregion
//#region ../node_modules/range-parser/index.js
/*!
* range-parser
* Copyright(c) 2012-2014 TJ Holowaychuk
* Copyright(c) 2015-2016 Douglas Christopher Wilson
* MIT Licensed
*/
var require_range_parser = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module exports.
	* @public
	*/
	module.exports = rangeParser;
	/**
	* Parse "Range" header `str` relative to the given file `size`.
	*
	* @param {Number} size
	* @param {String} str
	* @param {Object} [options]
	* @return {Array}
	* @public
	*/
	function rangeParser(size, str, options) {
		if (typeof str !== "string") throw new TypeError("argument str must be a string");
		var index = str.indexOf("=");
		if (index === -1) return -2;
		var arr = str.slice(index + 1).split(",");
		var ranges = [];
		ranges.type = str.slice(0, index);
		for (var i = 0; i < arr.length; i++) {
			var indexOf = arr[i].indexOf("-");
			if (indexOf === -1) return -2;
			var startStr = arr[i].slice(0, indexOf).trim();
			var endStr = arr[i].slice(indexOf + 1).trim();
			var start = parsePos(startStr);
			var end = parsePos(endStr);
			if (startStr.length === 0) {
				start = size - end;
				end = size - 1;
			} else if (endStr.length === 0) end = size - 1;
			if (end > size - 1) end = size - 1;
			if (isNaN(start) || isNaN(end)) return -2;
			if (start > end || start < 0) continue;
			ranges.push({
				start,
				end
			});
		}
		if (ranges.length < 1) return -1;
		return options && options.combine ? combineRanges(ranges) : ranges;
	}
	/**
	* Parse string to integer.
	* @private
	*/
	function parsePos(str) {
		if (/^\d+$/.test(str)) return Number(str);
		return NaN;
	}
	/**
	* Combine overlapping & adjacent ranges.
	* @private
	*/
	function combineRanges(ranges) {
		var ordered = ranges.map(mapWithIndex).sort(sortByRangeStart);
		for (var j = 0, i = 1; i < ordered.length; i++) {
			var range = ordered[i];
			var current = ordered[j];
			if (range.start > current.end + 1) ordered[++j] = range;
			else if (range.end > current.end) {
				current.end = range.end;
				current.index = Math.min(current.index, range.index);
			}
		}
		ordered.length = j + 1;
		var combined = ordered.sort(sortByRangeIndex).map(mapWithoutIndex);
		combined.type = ranges.type;
		return combined;
	}
	/**
	* Map function to add index value to ranges.
	* @private
	*/
	function mapWithIndex(range, index) {
		return {
			start: range.start,
			end: range.end,
			index
		};
	}
	/**
	* Map function to remove index value from ranges.
	* @private
	*/
	function mapWithoutIndex(range) {
		return {
			start: range.start,
			end: range.end
		};
	}
	/**
	* Sort function to sort ranges by index.
	* @private
	*/
	function sortByRangeIndex(a, b) {
		return a.index - b.index;
	}
	/**
	* Sort function to sort ranges by start position.
	* @private
	*/
	function sortByRangeStart(a, b) {
		return a.start - b.start;
	}
}));
//#endregion
//#region ../node_modules/express/lib/request.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_request = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var accepts = require_accepts();
	var isIP = __require("node:net").isIP;
	var typeis = require_type_is();
	var http$1 = __require("node:http");
	var fresh = require_fresh();
	var parseRange = require_range_parser();
	var parse = require_parseurl();
	var proxyaddr = require_proxy_addr();
	/**
	* Request prototype.
	* @public
	*/
	var req = Object.create(http$1.IncomingMessage.prototype);
	/**
	* Module exports.
	* @public
	*/
	module.exports = req;
	/**
	* Return request header.
	*
	* The `Referrer` header field is special-cased,
	* both `Referrer` and `Referer` are interchangeable.
	*
	* Examples:
	*
	*     req.get('Content-Type');
	*     // => "text/plain"
	*
	*     req.get('content-type');
	*     // => "text/plain"
	*
	*     req.get('Something');
	*     // => undefined
	*
	* Aliased as `req.header()`.
	*
	* @param {String} name
	* @return {String}
	* @public
	*/
	req.get = req.header = function header(name) {
		if (!name) throw new TypeError("name argument is required to req.get");
		if (typeof name !== "string") throw new TypeError("name must be a string to req.get");
		var lc = name.toLowerCase();
		switch (lc) {
			case "referer":
			case "referrer": return this.headers.referrer || this.headers.referer;
			default: return this.headers[lc];
		}
	};
	/**
	* To do: update docs.
	*
	* Check if the given `type(s)` is acceptable, returning
	* the best match when true, otherwise `undefined`, in which
	* case you should respond with 406 "Not Acceptable".
	*
	* The `type` value may be a single MIME type string
	* such as "application/json", an extension name
	* such as "json", a comma-delimited list such as "json, html, text/plain",
	* an argument list such as `"json", "html", "text/plain"`,
	* or an array `["json", "html", "text/plain"]`. When a list
	* or array is given, the _best_ match, if any is returned.
	*
	* Examples:
	*
	*     // Accept: text/html
	*     req.accepts('html');
	*     // => "html"
	*
	*     // Accept: text/*, application/json
	*     req.accepts('html');
	*     // => "html"
	*     req.accepts('text/html');
	*     // => "text/html"
	*     req.accepts('json, text');
	*     // => "json"
	*     req.accepts('application/json');
	*     // => "application/json"
	*
	*     // Accept: text/*, application/json
	*     req.accepts('image/png');
	*     req.accepts('png');
	*     // => undefined
	*
	*     // Accept: text/*;q=.5, application/json
	*     req.accepts(['html', 'json']);
	*     req.accepts('html', 'json');
	*     req.accepts('html, json');
	*     // => "json"
	*
	* @param {String|Array} type(s)
	* @return {String|Array|Boolean}
	* @public
	*/
	req.accepts = function() {
		var accept = accepts(this);
		return accept.types.apply(accept, arguments);
	};
	/**
	* Check if the given `encoding`s are accepted.
	*
	* @param {String} ...encoding
	* @return {String|Array}
	* @public
	*/
	req.acceptsEncodings = function() {
		var accept = accepts(this);
		return accept.encodings.apply(accept, arguments);
	};
	/**
	* Check if the given `charset`s are acceptable,
	* otherwise you should respond with 406 "Not Acceptable".
	*
	* @param {String} ...charset
	* @return {String|Array}
	* @public
	*/
	req.acceptsCharsets = function() {
		var accept = accepts(this);
		return accept.charsets.apply(accept, arguments);
	};
	/**
	* Check if the given `lang`s are acceptable,
	* otherwise you should respond with 406 "Not Acceptable".
	*
	* @param {String} ...lang
	* @return {String|Array}
	* @public
	*/
	req.acceptsLanguages = function(...languages) {
		return accepts(this).languages(...languages);
	};
	/**
	* Parse Range header field, capping to the given `size`.
	*
	* Unspecified ranges such as "0-" require knowledge of your resource length. In
	* the case of a byte range this is of course the total number of bytes. If the
	* Range header field is not given `undefined` is returned, `-1` when unsatisfiable,
	* and `-2` when syntactically invalid.
	*
	* When ranges are returned, the array has a "type" property which is the type of
	* range that is required (most commonly, "bytes"). Each array element is an object
	* with a "start" and "end" property for the portion of the range.
	*
	* The "combine" option can be set to `true` and overlapping & adjacent ranges
	* will be combined into a single range.
	*
	* NOTE: remember that ranges are inclusive, so for example "Range: users=0-3"
	* should respond with 4 users when available, not 3.
	*
	* @param {number} size
	* @param {object} [options]
	* @param {boolean} [options.combine=false]
	* @return {number|array}
	* @public
	*/
	req.range = function range(size, options) {
		var range = this.get("Range");
		if (!range) return;
		return parseRange(size, range, options);
	};
	/**
	* Parse the query string of `req.url`.
	*
	* This uses the "query parser" setting to parse the raw
	* string into an object.
	*
	* @return {String}
	* @api public
	*/
	defineGetter(req, "query", function query() {
		var queryparse = this.app.get("query parser fn");
		if (!queryparse) return Object.create(null);
		var querystring = parse(this).query;
		return queryparse(querystring);
	});
	/**
	* Check if the incoming request contains the "Content-Type"
	* header field, and it contains the given mime `type`.
	*
	* Examples:
	*
	*      // With Content-Type: text/html; charset=utf-8
	*      req.is('html');
	*      req.is('text/html');
	*      req.is('text/*');
	*      // => true
	*
	*      // When Content-Type is application/json
	*      req.is('json');
	*      req.is('application/json');
	*      req.is('application/*');
	*      // => true
	*
	*      req.is('html');
	*      // => false
	*
	* @param {String|Array} types...
	* @return {String|false|null}
	* @public
	*/
	req.is = function is(types) {
		var arr = types;
		if (!Array.isArray(types)) {
			arr = new Array(arguments.length);
			for (var i = 0; i < arr.length; i++) arr[i] = arguments[i];
		}
		return typeis(this, arr);
	};
	/**
	* Return the protocol string "http" or "https"
	* when requested with TLS. When the "trust proxy"
	* setting trusts the socket address, the
	* "X-Forwarded-Proto" header field will be trusted
	* and used if present.
	*
	* If you're running behind a reverse proxy that
	* supplies https for you this may be enabled.
	*
	* @return {String}
	* @public
	*/
	defineGetter(req, "protocol", function protocol() {
		var proto = this.socket.encrypted ? "https" : "http";
		if (!this.app.get("trust proxy fn")(this.socket.remoteAddress, 0)) return proto;
		var header = this.get("X-Forwarded-Proto") || proto;
		var index = header.indexOf(",");
		return index !== -1 ? header.substring(0, index).trim() : header.trim();
	});
	/**
	* Short-hand for:
	*
	*    req.protocol === 'https'
	*
	* @return {Boolean}
	* @public
	*/
	defineGetter(req, "secure", function secure() {
		return this.protocol === "https";
	});
	/**
	* Return the remote address from the trusted proxy.
	*
	* The is the remote address on the socket unless
	* "trust proxy" is set.
	*
	* @return {String}
	* @public
	*/
	defineGetter(req, "ip", function ip() {
		var trust = this.app.get("trust proxy fn");
		return proxyaddr(this, trust);
	});
	/**
	* When "trust proxy" is set, trusted proxy addresses + client.
	*
	* For example if the value were "client, proxy1, proxy2"
	* you would receive the array `["client", "proxy1", "proxy2"]`
	* where "proxy2" is the furthest down-stream and "proxy1" and
	* "proxy2" were trusted.
	*
	* @return {Array}
	* @public
	*/
	defineGetter(req, "ips", function ips() {
		var trust = this.app.get("trust proxy fn");
		var addrs = proxyaddr.all(this, trust);
		addrs.reverse().pop();
		return addrs;
	});
	/**
	* Return subdomains as an array.
	*
	* Subdomains are the dot-separated parts of the host before the main domain of
	* the app. By default, the domain of the app is assumed to be the last two
	* parts of the host. This can be changed by setting "subdomain offset".
	*
	* For example, if the domain is "tobi.ferrets.example.com":
	* If "subdomain offset" is not set, req.subdomains is `["ferrets", "tobi"]`.
	* If "subdomain offset" is 3, req.subdomains is `["tobi"]`.
	*
	* @return {Array}
	* @public
	*/
	defineGetter(req, "subdomains", function subdomains() {
		var hostname = this.hostname;
		if (!hostname) return [];
		var offset = this.app.get("subdomain offset");
		return (!isIP(hostname) ? hostname.split(".").reverse() : [hostname]).slice(offset);
	});
	/**
	* Short-hand for `url.parse(req.url).pathname`.
	*
	* @return {String}
	* @public
	*/
	defineGetter(req, "path", function path() {
		return parse(this).pathname;
	});
	/**
	* Parse the "Host" header field to a host.
	*
	* When the "trust proxy" setting trusts the socket
	* address, the "X-Forwarded-Host" header field will
	* be trusted.
	*
	* @return {String}
	* @public
	*/
	defineGetter(req, "host", function host() {
		var trust = this.app.get("trust proxy fn");
		var val = this.get("X-Forwarded-Host");
		if (!val || !trust(this.socket.remoteAddress, 0)) val = this.get("Host");
		else if (val.indexOf(",") !== -1) val = val.substring(0, val.indexOf(",")).trimRight();
		return val || void 0;
	});
	/**
	* Parse the "Host" header field to a hostname.
	*
	* When the "trust proxy" setting trusts the socket
	* address, the "X-Forwarded-Host" header field will
	* be trusted.
	*
	* @return {String}
	* @api public
	*/
	defineGetter(req, "hostname", function hostname() {
		var host = this.host;
		if (!host) return;
		var offset = host[0] === "[" ? host.indexOf("]") + 1 : 0;
		var index = host.indexOf(":", offset);
		return index !== -1 ? host.substring(0, index) : host;
	});
	/**
	* Check if the request is fresh, aka
	* Last-Modified or the ETag
	* still match.
	*
	* @return {Boolean}
	* @public
	*/
	defineGetter(req, "fresh", function() {
		var method = this.method;
		var res = this.res;
		var status = res.statusCode;
		if ("GET" !== method && "HEAD" !== method) return false;
		if (status >= 200 && status < 300 || 304 === status) return fresh(this.headers, {
			"etag": res.get("ETag"),
			"last-modified": res.get("Last-Modified")
		});
		return false;
	});
	/**
	* Check if the request is stale, aka
	* "Last-Modified" and / or the "ETag" for the
	* resource has changed.
	*
	* @return {Boolean}
	* @public
	*/
	defineGetter(req, "stale", function stale() {
		return !this.fresh;
	});
	/**
	* Check if the request was an _XMLHttpRequest_.
	*
	* @return {Boolean}
	* @public
	*/
	defineGetter(req, "xhr", function xhr() {
		return (this.get("X-Requested-With") || "").toLowerCase() === "xmlhttprequest";
	});
	/**
	* Helper function for creating a getter on an object.
	*
	* @param {Object} obj
	* @param {String} name
	* @param {Function} getter
	* @private
	*/
	function defineGetter(obj, name, getter) {
		Object.defineProperty(obj, name, {
			configurable: true,
			enumerable: true,
			get: getter
		});
	}
}));
//#endregion
//#region ../node_modules/send/index.js
/*!
* send
* Copyright(c) 2012 TJ Holowaychuk
* Copyright(c) 2014-2022 Douglas Christopher Wilson
* MIT Licensed
*/
var require_send = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var createError = require_http_errors();
	var debug = require_src()("send");
	var encodeUrl = require_encodeurl();
	var escapeHtml = require_escape_html();
	var etag = require_etag();
	var fresh = require_fresh();
	var fs = __require("fs");
	var mime = require_mime_types();
	var ms = require_ms();
	var onFinished = require_on_finished();
	var parseRange = require_range_parser();
	var path$1 = __require("path");
	var statuses = require_statuses();
	var Stream = __require("stream");
	var util = __require("util");
	/**
	* Path function references.
	* @private
	*/
	var extname = path$1.extname;
	var join = path$1.join;
	var normalize = path$1.normalize;
	var resolve = path$1.resolve;
	var sep = path$1.sep;
	/**
	* Regular expression for identifying a bytes Range header.
	* @private
	*/
	var BYTES_RANGE_REGEXP = /^ *bytes=/;
	/**
	* Maximum value allowed for the max age.
	* @private
	*/
	var MAX_MAXAGE = 31536e6;
	/**
	* Regular expression to match a path with a directory up component.
	* @private
	*/
	var UP_PATH_REGEXP = /(?:^|[\\/])\.\.(?:[\\/]|$)/;
	/**
	* Module exports.
	* @public
	*/
	module.exports = send;
	/**
	* Return a `SendStream` for `req` and `path`.
	*
	* @param {object} req
	* @param {string} path
	* @param {object} [options]
	* @return {SendStream}
	* @public
	*/
	function send(req, path, options) {
		return new SendStream(req, path, options);
	}
	/**
	* Initialize a `SendStream` with the given `path`.
	*
	* @param {Request} req
	* @param {String} path
	* @param {object} [options]
	* @private
	*/
	function SendStream(req, path, options) {
		Stream.call(this);
		var opts = options || {};
		this.options = opts;
		this.path = path;
		this.req = req;
		this._acceptRanges = opts.acceptRanges !== void 0 ? Boolean(opts.acceptRanges) : true;
		this._cacheControl = opts.cacheControl !== void 0 ? Boolean(opts.cacheControl) : true;
		this._etag = opts.etag !== void 0 ? Boolean(opts.etag) : true;
		this._dotfiles = opts.dotfiles !== void 0 ? opts.dotfiles : "ignore";
		if (this._dotfiles !== "ignore" && this._dotfiles !== "allow" && this._dotfiles !== "deny") throw new TypeError("dotfiles option must be \"allow\", \"deny\", or \"ignore\"");
		this._extensions = opts.extensions !== void 0 ? normalizeList(opts.extensions, "extensions option") : [];
		this._immutable = opts.immutable !== void 0 ? Boolean(opts.immutable) : false;
		this._index = opts.index !== void 0 ? normalizeList(opts.index, "index option") : ["index.html"];
		this._lastModified = opts.lastModified !== void 0 ? Boolean(opts.lastModified) : true;
		this._maxage = opts.maxAge || opts.maxage;
		this._maxage = typeof this._maxage === "string" ? ms(this._maxage) : Number(this._maxage);
		this._maxage = !isNaN(this._maxage) ? Math.min(Math.max(0, this._maxage), MAX_MAXAGE) : 0;
		this._root = opts.root ? resolve(opts.root) : null;
	}
	/**
	* Inherits from `Stream`.
	*/
	util.inherits(SendStream, Stream);
	/**
	* Emit error with `status`.
	*
	* @param {number} status
	* @param {Error} [err]
	* @private
	*/
	SendStream.prototype.error = function error(status, err) {
		if (hasListeners(this, "error")) return this.emit("error", createHttpError(status, err));
		var res = this.res;
		var doc = createHtmlDocument("Error", escapeHtml(statuses.message[status] || String(status)));
		clearHeaders(res);
		if (err && err.headers) setHeaders(res, err.headers);
		res.statusCode = status;
		res.setHeader("Content-Type", "text/html; charset=UTF-8");
		res.setHeader("Content-Length", Buffer.byteLength(doc));
		res.setHeader("Content-Security-Policy", "default-src 'none'");
		res.setHeader("X-Content-Type-Options", "nosniff");
		res.end(doc);
	};
	/**
	* Check if the pathname ends with "/".
	*
	* @return {boolean}
	* @private
	*/
	SendStream.prototype.hasTrailingSlash = function hasTrailingSlash() {
		return this.path[this.path.length - 1] === "/";
	};
	/**
	* Check if this is a conditional GET request.
	*
	* @return {Boolean}
	* @api private
	*/
	SendStream.prototype.isConditionalGET = function isConditionalGET() {
		return this.req.headers["if-match"] || this.req.headers["if-unmodified-since"] || this.req.headers["if-none-match"] || this.req.headers["if-modified-since"];
	};
	/**
	* Check if the request preconditions failed.
	*
	* @return {boolean}
	* @private
	*/
	SendStream.prototype.isPreconditionFailure = function isPreconditionFailure() {
		var req = this.req;
		var res = this.res;
		var match = req.headers["if-match"];
		if (match) {
			var etag = res.getHeader("ETag");
			return !etag || match !== "*" && parseTokenList(match).every(function(match) {
				return match !== etag && match !== "W/" + etag && "W/" + match !== etag;
			});
		}
		var unmodifiedSince = parseHttpDate(req.headers["if-unmodified-since"]);
		if (!isNaN(unmodifiedSince)) {
			var lastModified = parseHttpDate(res.getHeader("Last-Modified"));
			return isNaN(lastModified) || lastModified > unmodifiedSince;
		}
		return false;
	};
	/**
	* Strip various content header fields for a change in entity.
	*
	* @private
	*/
	SendStream.prototype.removeContentHeaderFields = function removeContentHeaderFields() {
		var res = this.res;
		res.removeHeader("Content-Encoding");
		res.removeHeader("Content-Language");
		res.removeHeader("Content-Length");
		res.removeHeader("Content-Range");
		res.removeHeader("Content-Type");
	};
	/**
	* Respond with 304 not modified.
	*
	* @api private
	*/
	SendStream.prototype.notModified = function notModified() {
		var res = this.res;
		debug("not modified");
		this.removeContentHeaderFields();
		res.statusCode = 304;
		res.end();
	};
	/**
	* Raise error that headers already sent.
	*
	* @api private
	*/
	SendStream.prototype.headersAlreadySent = function headersAlreadySent() {
		var err = /* @__PURE__ */ new Error("Can't set headers after they are sent.");
		debug("headers already sent");
		this.error(500, err);
	};
	/**
	* Check if the request is cacheable, aka
	* responded with 2xx or 304 (see RFC 2616 section 14.2{5,6}).
	*
	* @return {Boolean}
	* @api private
	*/
	SendStream.prototype.isCachable = function isCachable() {
		var statusCode = this.res.statusCode;
		return statusCode >= 200 && statusCode < 300 || statusCode === 304;
	};
	/**
	* Handle stat() error.
	*
	* @param {Error} error
	* @private
	*/
	SendStream.prototype.onStatError = function onStatError(error) {
		switch (error.code) {
			case "ENAMETOOLONG":
			case "ENOENT":
			case "ENOTDIR":
				this.error(404, error);
				break;
			default: this.error(500, error);
		}
	};
	/**
	* Check if the cache is fresh.
	*
	* @return {Boolean}
	* @api private
	*/
	SendStream.prototype.isFresh = function isFresh() {
		return fresh(this.req.headers, {
			etag: this.res.getHeader("ETag"),
			"last-modified": this.res.getHeader("Last-Modified")
		});
	};
	/**
	* Check if the range is fresh.
	*
	* @return {Boolean}
	* @api private
	*/
	SendStream.prototype.isRangeFresh = function isRangeFresh() {
		var ifRange = this.req.headers["if-range"];
		if (!ifRange) return true;
		if (ifRange.indexOf("\"") !== -1) {
			var etag = this.res.getHeader("ETag");
			return Boolean(etag && ifRange.indexOf(etag) !== -1);
		}
		return parseHttpDate(this.res.getHeader("Last-Modified")) <= parseHttpDate(ifRange);
	};
	/**
	* Redirect to path.
	*
	* @param {string} path
	* @private
	*/
	SendStream.prototype.redirect = function redirect(path) {
		var res = this.res;
		if (hasListeners(this, "directory")) {
			this.emit("directory", res, path);
			return;
		}
		if (this.hasTrailingSlash()) {
			this.error(403);
			return;
		}
		var loc = encodeUrl(collapseLeadingSlashes(this.path + "/"));
		var doc = createHtmlDocument("Redirecting", "Redirecting to " + escapeHtml(loc));
		res.statusCode = 301;
		res.setHeader("Content-Type", "text/html; charset=UTF-8");
		res.setHeader("Content-Length", Buffer.byteLength(doc));
		res.setHeader("Content-Security-Policy", "default-src 'none'");
		res.setHeader("X-Content-Type-Options", "nosniff");
		res.setHeader("Location", loc);
		res.end(doc);
	};
	/**
	* Pipe to `res.
	*
	* @param {Stream} res
	* @return {Stream} res
	* @api public
	*/
	SendStream.prototype.pipe = function pipe(res) {
		var root = this._root;
		this.res = res;
		var path = decode(this.path);
		if (path === -1) {
			this.error(400);
			return res;
		}
		if (~path.indexOf("\0")) {
			this.error(400);
			return res;
		}
		var parts;
		if (root !== null) {
			if (path) path = normalize("." + sep + path);
			if (UP_PATH_REGEXP.test(path)) {
				debug("malicious path \"%s\"", path);
				this.error(403);
				return res;
			}
			parts = path.split(sep);
			path = normalize(join(root, path));
		} else {
			if (UP_PATH_REGEXP.test(path)) {
				debug("malicious path \"%s\"", path);
				this.error(403);
				return res;
			}
			parts = normalize(path).split(sep);
			path = resolve(path);
		}
		if (containsDotFile(parts)) {
			debug("%s dotfile \"%s\"", this._dotfiles, path);
			switch (this._dotfiles) {
				case "allow": break;
				case "deny":
					this.error(403);
					return res;
				default:
					this.error(404);
					return res;
			}
		}
		if (this._index.length && this.hasTrailingSlash()) {
			this.sendIndex(path);
			return res;
		}
		this.sendFile(path);
		return res;
	};
	/**
	* Transfer `path`.
	*
	* @param {String} path
	* @api public
	*/
	SendStream.prototype.send = function send(path, stat) {
		var len = stat.size;
		var options = this.options;
		var opts = {};
		var res = this.res;
		var req = this.req;
		var ranges = req.headers.range;
		var offset = options.start || 0;
		if (res.headersSent) {
			this.headersAlreadySent();
			return;
		}
		debug("pipe \"%s\"", path);
		this.setHeader(path, stat);
		this.type(path);
		if (this.isConditionalGET()) {
			if (this.isPreconditionFailure()) {
				this.error(412);
				return;
			}
			if (this.isCachable() && this.isFresh()) {
				this.notModified();
				return;
			}
		}
		len = Math.max(0, len - offset);
		if (options.end !== void 0) {
			var bytes = options.end - offset + 1;
			if (len > bytes) len = bytes;
		}
		if (this._acceptRanges && BYTES_RANGE_REGEXP.test(ranges)) {
			ranges = parseRange(len, ranges, { combine: true });
			if (!this.isRangeFresh()) {
				debug("range stale");
				ranges = -2;
			}
			if (ranges === -1) {
				debug("range unsatisfiable");
				res.setHeader("Content-Range", contentRange("bytes", len));
				return this.error(416, { headers: { "Content-Range": res.getHeader("Content-Range") } });
			}
			if (ranges !== -2 && ranges.length === 1) {
				debug("range %j", ranges);
				res.statusCode = 206;
				res.setHeader("Content-Range", contentRange("bytes", len, ranges[0]));
				offset += ranges[0].start;
				len = ranges[0].end - ranges[0].start + 1;
			}
		}
		for (var prop in options) opts[prop] = options[prop];
		opts.start = offset;
		opts.end = Math.max(offset, offset + len - 1);
		res.setHeader("Content-Length", len);
		if (req.method === "HEAD") {
			res.end();
			return;
		}
		this.stream(path, opts);
	};
	/**
	* Transfer file for `path`.
	*
	* @param {String} path
	* @api private
	*/
	SendStream.prototype.sendFile = function sendFile(path) {
		var i = 0;
		var self = this;
		debug("stat \"%s\"", path);
		fs.stat(path, function onstat(err, stat) {
			var pathEndsWithSep = path[path.length - 1] === sep;
			if (err && err.code === "ENOENT" && !extname(path) && !pathEndsWithSep) return next(err);
			if (err) return self.onStatError(err);
			if (stat.isDirectory()) return self.redirect(path);
			if (pathEndsWithSep) return self.error(404);
			self.emit("file", path, stat);
			self.send(path, stat);
		});
		function next(err) {
			if (self._extensions.length <= i) return err ? self.onStatError(err) : self.error(404);
			var p = path + "." + self._extensions[i++];
			debug("stat \"%s\"", p);
			fs.stat(p, function(err, stat) {
				if (err) return next(err);
				if (stat.isDirectory()) return next();
				self.emit("file", p, stat);
				self.send(p, stat);
			});
		}
	};
	/**
	* Transfer index for `path`.
	*
	* @param {String} path
	* @api private
	*/
	SendStream.prototype.sendIndex = function sendIndex(path) {
		var i = -1;
		var self = this;
		function next(err) {
			if (++i >= self._index.length) {
				if (err) return self.onStatError(err);
				return self.error(404);
			}
			var p = join(path, self._index[i]);
			debug("stat \"%s\"", p);
			fs.stat(p, function(err, stat) {
				if (err) return next(err);
				if (stat.isDirectory()) return next();
				self.emit("file", p, stat);
				self.send(p, stat);
			});
		}
		next();
	};
	/**
	* Stream `path` to the response.
	*
	* @param {String} path
	* @param {Object} options
	* @api private
	*/
	SendStream.prototype.stream = function stream(path, options) {
		var self = this;
		var res = this.res;
		var stream = fs.createReadStream(path, options);
		this.emit("stream", stream);
		stream.pipe(res);
		function cleanup() {
			stream.destroy();
		}
		onFinished(res, cleanup);
		stream.on("error", function onerror(err) {
			cleanup();
			self.onStatError(err);
		});
		stream.on("end", function onend() {
			self.emit("end");
		});
	};
	/**
	* Set content-type based on `path`
	* if it hasn't been explicitly set.
	*
	* @param {String} path
	* @api private
	*/
	SendStream.prototype.type = function type(path) {
		var res = this.res;
		if (res.getHeader("Content-Type")) return;
		var ext = extname(path);
		var type = mime.contentType(ext) || "application/octet-stream";
		debug("content-type %s", type);
		res.setHeader("Content-Type", type);
	};
	/**
	* Set response header fields, most
	* fields may be pre-defined.
	*
	* @param {String} path
	* @param {Object} stat
	* @api private
	*/
	SendStream.prototype.setHeader = function setHeader(path, stat) {
		var res = this.res;
		this.emit("headers", res, path, stat);
		if (this._acceptRanges && !res.getHeader("Accept-Ranges")) {
			debug("accept ranges");
			res.setHeader("Accept-Ranges", "bytes");
		}
		if (this._cacheControl && !res.getHeader("Cache-Control")) {
			var cacheControl = "public, max-age=" + Math.floor(this._maxage / 1e3);
			if (this._immutable) cacheControl += ", immutable";
			debug("cache-control %s", cacheControl);
			res.setHeader("Cache-Control", cacheControl);
		}
		if (this._lastModified && !res.getHeader("Last-Modified")) {
			var modified = stat.mtime.toUTCString();
			debug("modified %s", modified);
			res.setHeader("Last-Modified", modified);
		}
		if (this._etag && !res.getHeader("ETag")) {
			var val = etag(stat);
			debug("etag %s", val);
			res.setHeader("ETag", val);
		}
	};
	/**
	* Clear all headers from a response.
	*
	* @param {object} res
	* @private
	*/
	function clearHeaders(res) {
		for (const header of res.getHeaderNames()) res.removeHeader(header);
	}
	/**
	* Collapse all leading slashes into a single slash
	*
	* @param {string} str
	* @private
	*/
	function collapseLeadingSlashes(str) {
		for (var i = 0; i < str.length; i++) if (str[i] !== "/") break;
		return i > 1 ? "/" + str.substr(i) : str;
	}
	/**
	* Determine if path parts contain a dotfile.
	*
	* @api private
	*/
	function containsDotFile(parts) {
		for (var i = 0; i < parts.length; i++) {
			var part = parts[i];
			if (part.length > 1 && part[0] === ".") return true;
		}
		return false;
	}
	/**
	* Create a Content-Range header.
	*
	* @param {string} type
	* @param {number} size
	* @param {array} [range]
	*/
	function contentRange(type, size, range) {
		return type + " " + (range ? range.start + "-" + range.end : "*") + "/" + size;
	}
	/**
	* Create a minimal HTML document.
	*
	* @param {string} title
	* @param {string} body
	* @private
	*/
	function createHtmlDocument(title, body) {
		return "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>" + title + "</title>\n</head>\n<body>\n<pre>" + body + "</pre>\n</body>\n</html>\n";
	}
	/**
	* Create a HttpError object from simple arguments.
	*
	* @param {number} status
	* @param {Error|object} err
	* @private
	*/
	function createHttpError(status, err) {
		if (!err) return createError(status);
		return err instanceof Error ? createError(status, err, { expose: false }) : createError(status, err);
	}
	/**
	* decodeURIComponent.
	*
	* Allows V8 to only deoptimize this fn instead of all
	* of send().
	*
	* @param {String} path
	* @api private
	*/
	function decode(path) {
		try {
			return decodeURIComponent(path);
		} catch (err) {
			return -1;
		}
	}
	/**
	* Determine if emitter has listeners of a given type.
	*
	* The way to do this check is done three different ways in Node.js >= 0.10
	* so this consolidates them into a minimal set using instance methods.
	*
	* @param {EventEmitter} emitter
	* @param {string} type
	* @returns {boolean}
	* @private
	*/
	function hasListeners(emitter, type) {
		return (typeof emitter.listenerCount !== "function" ? emitter.listeners(type).length : emitter.listenerCount(type)) > 0;
	}
	/**
	* Normalize the index option into an array.
	*
	* @param {boolean|string|array} val
	* @param {string} name
	* @private
	*/
	function normalizeList(val, name) {
		var list = [].concat(val || []);
		for (var i = 0; i < list.length; i++) if (typeof list[i] !== "string") throw new TypeError(name + " must be array of strings or false");
		return list;
	}
	/**
	* Parse an HTTP Date into a number.
	*
	* @param {string} date
	* @private
	*/
	function parseHttpDate(date) {
		var timestamp = date && Date.parse(date);
		return typeof timestamp === "number" ? timestamp : NaN;
	}
	/**
	* Parse a HTTP token list.
	*
	* @param {string} str
	* @private
	*/
	function parseTokenList(str) {
		var end = 0;
		var list = [];
		var start = 0;
		for (var i = 0, len = str.length; i < len; i++) switch (str.charCodeAt(i)) {
			case 32:
				if (start === end) start = end = i + 1;
				break;
			case 44:
				if (start !== end) list.push(str.substring(start, end));
				start = end = i + 1;
				break;
			default: end = i + 1;
		}
		if (start !== end) list.push(str.substring(start, end));
		return list;
	}
	/**
	* Set an object of headers on a response.
	*
	* @param {object} res
	* @param {object} headers
	* @private
	*/
	function setHeaders(res, headers) {
		var keys = Object.keys(headers);
		for (var i = 0; i < keys.length; i++) {
			var key = keys[i];
			res.setHeader(key, headers[key]);
		}
	}
}));
//#endregion
//#region ../node_modules/vary/index.js
/*!
* vary
* Copyright(c) 2014-2017 Douglas Christopher Wilson
* MIT Licensed
*/
var require_vary = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module exports.
	*/
	module.exports = vary;
	module.exports.append = append;
	/**
	* RegExp to match field-name in RFC 7230 sec 3.2
	*
	* field-name    = token
	* token         = 1*tchar
	* tchar         = "!" / "#" / "$" / "%" / "&" / "'" / "*"
	*               / "+" / "-" / "." / "^" / "_" / "`" / "|" / "~"
	*               / DIGIT / ALPHA
	*               ; any VCHAR, except delimiters
	*/
	var FIELD_NAME_REGEXP = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
	/**
	* Append a field to a vary header.
	*
	* @param {String} header
	* @param {String|Array} field
	* @return {String}
	* @public
	*/
	function append(header, field) {
		if (typeof header !== "string") throw new TypeError("header argument is required");
		if (!field) throw new TypeError("field argument is required");
		var fields = !Array.isArray(field) ? parse(String(field)) : field;
		for (var j = 0; j < fields.length; j++) if (!FIELD_NAME_REGEXP.test(fields[j])) throw new TypeError("field argument contains an invalid header name");
		if (header === "*") return header;
		var val = header;
		var vals = parse(header.toLowerCase());
		if (fields.indexOf("*") !== -1 || vals.indexOf("*") !== -1) return "*";
		for (var i = 0; i < fields.length; i++) {
			var fld = fields[i].toLowerCase();
			if (vals.indexOf(fld) === -1) {
				vals.push(fld);
				val = val ? val + ", " + fields[i] : fields[i];
			}
		}
		return val;
	}
	/**
	* Parse a vary header into an array.
	*
	* @param {String} header
	* @return {Array}
	* @private
	*/
	function parse(header) {
		var end = 0;
		var list = [];
		var start = 0;
		for (var i = 0, len = header.length; i < len; i++) switch (header.charCodeAt(i)) {
			case 32:
				if (start === end) start = end = i + 1;
				break;
			case 44:
				list.push(header.substring(start, end));
				start = end = i + 1;
				break;
			default: end = i + 1;
		}
		list.push(header.substring(start, end));
		return list;
	}
	/**
	* Mark that a request is varied on a header field.
	*
	* @param {Object} res
	* @param {String|Array} field
	* @public
	*/
	function vary(res, field) {
		if (!res || !res.getHeader || !res.setHeader) throw new TypeError("res argument is required");
		var val = res.getHeader("Vary") || "";
		if (val = append(Array.isArray(val) ? val.join(", ") : String(val), field)) res.setHeader("Vary", val);
	}
}));
//#endregion
//#region ../node_modules/express/lib/response.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_response = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var contentDisposition = require_content_disposition();
	var createError = require_http_errors();
	var deprecate = require_depd()("express");
	var encodeUrl = require_encodeurl();
	var escapeHtml = require_escape_html();
	var http = __require("node:http");
	var onFinished = require_on_finished();
	var mime = require_mime_types();
	var path = __require("node:path");
	var pathIsAbsolute = __require("node:path").isAbsolute;
	var statuses = require_statuses();
	var sign = require_cookie_signature().sign;
	var normalizeType = require_utils().normalizeType;
	var normalizeTypes = require_utils().normalizeTypes;
	var setCharset = require_utils().setCharset;
	var cookie = require_cookie();
	var send = require_send();
	var extname = path.extname;
	var resolve = path.resolve;
	var vary = require_vary();
	const { Buffer: Buffer$1 } = __require("node:buffer");
	/**
	* Response prototype.
	* @public
	*/
	var res = Object.create(http.ServerResponse.prototype);
	/**
	* Module exports.
	* @public
	*/
	module.exports = res;
	/**
	* Set the HTTP status code for the response.
	*
	* Expects an integer value between 100 and 999 inclusive.
	* Throws an error if the provided status code is not an integer or if it's outside the allowable range.
	*
	* @param {number} code - The HTTP status code to set.
	* @return {ServerResponse} - Returns itself for chaining methods.
	* @throws {TypeError} If `code` is not an integer.
	* @throws {RangeError} If `code` is outside the range 100 to 999.
	* @public
	*/
	res.status = function status(code) {
		if (!Number.isInteger(code)) throw new TypeError(`Invalid status code: ${JSON.stringify(code)}. Status code must be an integer.`);
		if (code < 100 || code > 999) throw new RangeError(`Invalid status code: ${JSON.stringify(code)}. Status code must be greater than 99 and less than 1000.`);
		this.statusCode = code;
		return this;
	};
	/**
	* Set Link header field with the given `links`.
	*
	* Examples:
	*
	*    res.links({
	*      next: 'http://api.example.com/users?page=2',
	*      last: 'http://api.example.com/users?page=5',
	*      pages: [
	*        'http://api.example.com/users?page=1',
	*        'http://api.example.com/users?page=2'
	*      ]
	*    });
	*
	* @param {Object} links
	* @return {ServerResponse}
	* @public
	*/
	res.links = function(links) {
		var link = this.get("Link") || "";
		if (link) link += ", ";
		return this.set("Link", link + Object.keys(links).map(function(rel) {
			if (Array.isArray(links[rel])) return links[rel].map(function(singleLink) {
				return `<${singleLink}>; rel="${rel}"`;
			}).join(", ");
			else return `<${links[rel]}>; rel="${rel}"`;
		}).join(", "));
	};
	/**
	* Send a response.
	*
	* Examples:
	*
	*     res.send(Buffer.from('wahoo'));
	*     res.send({ some: 'json' });
	*     res.send('<p>some html</p>');
	*
	* @param {string|number|boolean|object|Buffer} body
	* @public
	*/
	res.send = function send(body) {
		var chunk = body;
		var encoding;
		var req = this.req;
		var type;
		var app = this.app;
		switch (typeof chunk) {
			case "string":
				if (!this.get("Content-Type")) this.type("html");
				break;
			case "boolean":
			case "number":
			case "object": if (chunk === null) chunk = "";
			else if (ArrayBuffer.isView(chunk)) {
				if (!this.get("Content-Type")) this.type("bin");
			} else return this.json(chunk);
		}
		if (typeof chunk === "string") {
			encoding = "utf8";
			type = this.get("Content-Type");
			if (typeof type === "string") this.set("Content-Type", setCharset(type, "utf-8"));
		}
		var etagFn = app.get("etag fn");
		var generateETag = !this.get("ETag") && typeof etagFn === "function";
		var len;
		if (chunk !== void 0) {
			if (Buffer$1.isBuffer(chunk)) len = chunk.length;
			else if (!generateETag && chunk.length < 1e3) len = Buffer$1.byteLength(chunk, encoding);
			else {
				chunk = Buffer$1.from(chunk, encoding);
				encoding = void 0;
				len = chunk.length;
			}
			this.set("Content-Length", len);
		}
		var etag;
		if (generateETag && len !== void 0) {
			if (etag = etagFn(chunk, encoding)) this.set("ETag", etag);
		}
		if (req.fresh) this.status(304);
		if (204 === this.statusCode || 304 === this.statusCode) {
			this.removeHeader("Content-Type");
			this.removeHeader("Content-Length");
			this.removeHeader("Transfer-Encoding");
			chunk = "";
		}
		if (this.statusCode === 205) {
			this.set("Content-Length", "0");
			this.removeHeader("Transfer-Encoding");
			chunk = "";
		}
		if (req.method === "HEAD") this.end();
		else this.end(chunk, encoding);
		return this;
	};
	/**
	* Send JSON response.
	*
	* Examples:
	*
	*     res.json(null);
	*     res.json({ user: 'tj' });
	*
	* @param {string|number|boolean|object} obj
	* @public
	*/
	res.json = function json(obj) {
		var app = this.app;
		var escape = app.get("json escape");
		var body = stringify(obj, app.get("json replacer"), app.get("json spaces"), escape);
		if (!this.get("Content-Type")) this.set("Content-Type", "application/json");
		return this.send(body);
	};
	/**
	* Send JSON response with JSONP callback support.
	*
	* Examples:
	*
	*     res.jsonp(null);
	*     res.jsonp({ user: 'tj' });
	*
	* @param {string|number|boolean|object} obj
	* @public
	*/
	res.jsonp = function jsonp(obj) {
		var app = this.app;
		var escape = app.get("json escape");
		var body = stringify(obj, app.get("json replacer"), app.get("json spaces"), escape);
		var callback = this.req.query[app.get("jsonp callback name")];
		if (!this.get("Content-Type")) {
			this.set("X-Content-Type-Options", "nosniff");
			this.set("Content-Type", "application/json");
		}
		if (Array.isArray(callback)) callback = callback[0];
		if (typeof callback === "string" && callback.length !== 0) {
			this.set("X-Content-Type-Options", "nosniff");
			this.set("Content-Type", "text/javascript");
			callback = callback.replace(/[^\[\]\w$.]/g, "");
			if (body === void 0) body = "";
			else if (typeof body === "string") body = body.replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
			body = "/**/ typeof " + callback + " === 'function' && " + callback + "(" + body + ");";
		}
		return this.send(body);
	};
	/**
	* Send given HTTP status code.
	*
	* Sets the response status to `statusCode` and the body of the
	* response to the standard description from node's http.STATUS_CODES
	* or the statusCode number if no description.
	*
	* Examples:
	*
	*     res.sendStatus(200);
	*
	* @param {number} statusCode
	* @public
	*/
	res.sendStatus = function sendStatus(statusCode) {
		var body = statuses.message[statusCode] || String(statusCode);
		this.status(statusCode);
		this.type("txt");
		return this.send(body);
	};
	/**
	* Transfer the file at the given `path`.
	*
	* Automatically sets the _Content-Type_ response header field.
	* The callback `callback(err)` is invoked when the transfer is complete
	* or when an error occurs. Be sure to check `res.headersSent`
	* if you wish to attempt responding, as the header and some data
	* may have already been transferred.
	*
	* Options:
	*
	*   - `maxAge`   defaulting to 0 (can be string converted by `ms`)
	*   - `root`     root directory for relative filenames
	*   - `headers`  object of headers to serve with file
	*   - `dotfiles` serve dotfiles, defaulting to false; can be `"allow"` to send them
	*
	* Other options are passed along to `send`.
	*
	* Examples:
	*
	*  The following example illustrates how `res.sendFile()` may
	*  be used as an alternative for the `static()` middleware for
	*  dynamic situations. The code backing `res.sendFile()` is actually
	*  the same code, so HTTP cache support etc is identical.
	*
	*     app.get('/user/:uid/photos/:file', function(req, res){
	*       var uid = req.params.uid
	*         , file = req.params.file;
	*
	*       req.user.mayViewFilesFrom(uid, function(yes){
	*         if (yes) {
	*           res.sendFile('/uploads/' + uid + '/' + file);
	*         } else {
	*           res.send(403, 'Sorry! you cant see that.');
	*         }
	*       });
	*     });
	*
	* @public
	*/
	res.sendFile = function sendFile(path, options, callback) {
		var done = callback;
		var req = this.req;
		var res = this;
		var next = req.next;
		var opts = options || {};
		if (!path) throw new TypeError("path argument is required to res.sendFile");
		if (typeof path !== "string") throw new TypeError("path must be a string to res.sendFile");
		if (typeof options === "function") {
			done = options;
			opts = {};
		}
		if (!opts.root && !pathIsAbsolute(path)) throw new TypeError("path must be absolute or specify root to res.sendFile");
		var pathname = encodeURI(path);
		opts.etag = this.app.enabled("etag");
		sendfile(res, send(req, pathname, opts), opts, function(err) {
			if (done) return done(err);
			if (err && err.code === "EISDIR") return next();
			if (err && err.code !== "ECONNABORTED" && err.syscall !== "write") next(err);
		});
	};
	/**
	* Transfer the file at the given `path` as an attachment.
	*
	* Optionally providing an alternate attachment `filename`,
	* and optional callback `callback(err)`. The callback is invoked
	* when the data transfer is complete, or when an error has
	* occurred. Be sure to check `res.headersSent` if you plan to respond.
	*
	* Optionally providing an `options` object to use with `res.sendFile()`.
	* This function will set the `Content-Disposition` header, overriding
	* any `Content-Disposition` header passed as header options in order
	* to set the attachment and filename.
	*
	* This method uses `res.sendFile()`.
	*
	* @public
	*/
	res.download = function download(path, filename, options, callback) {
		var done = callback;
		var name = filename;
		var opts = options || null;
		if (typeof filename === "function") {
			done = filename;
			name = null;
			opts = null;
		} else if (typeof options === "function") {
			done = options;
			opts = null;
		}
		if (typeof filename === "object" && (typeof options === "function" || options === void 0)) {
			name = null;
			opts = filename;
		}
		var headers = { "Content-Disposition": contentDisposition(name || path) };
		if (opts && opts.headers) {
			var keys = Object.keys(opts.headers);
			for (var i = 0; i < keys.length; i++) {
				var key = keys[i];
				if (key.toLowerCase() !== "content-disposition") headers[key] = opts.headers[key];
			}
		}
		opts = Object.create(opts);
		opts.headers = headers;
		var fullPath = !opts.root ? resolve(path) : path;
		return this.sendFile(fullPath, opts, done);
	};
	/**
	* Set _Content-Type_ response header with `type` through `mime.contentType()`
	* when it does not contain "/", or set the Content-Type to `type` otherwise.
	* When no mapping is found though `mime.contentType()`, the type is set to
	* "application/octet-stream".
	*
	* Examples:
	*
	*     res.type('.html');
	*     res.type('html');
	*     res.type('json');
	*     res.type('application/json');
	*     res.type('png');
	*
	* @param {String} type
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.contentType = res.type = function contentType(type) {
		var ct = type.indexOf("/") === -1 ? mime.contentType(type) || "application/octet-stream" : type;
		return this.set("Content-Type", ct);
	};
	/**
	* Respond to the Acceptable formats using an `obj`
	* of mime-type callbacks.
	*
	* This method uses `req.accepted`, an array of
	* acceptable types ordered by their quality values.
	* When "Accept" is not present the _first_ callback
	* is invoked, otherwise the first match is used. When
	* no match is performed the server responds with
	* 406 "Not Acceptable".
	*
	* Content-Type is set for you, however if you choose
	* you may alter this within the callback using `res.type()`
	* or `res.set('Content-Type', ...)`.
	*
	*    res.format({
	*      'text/plain': function(){
	*        res.send('hey');
	*      },
	*
	*      'text/html': function(){
	*        res.send('<p>hey</p>');
	*      },
	*
	*      'application/json': function () {
	*        res.send({ message: 'hey' });
	*      }
	*    });
	*
	* In addition to canonicalized MIME types you may
	* also use extnames mapped to these types:
	*
	*    res.format({
	*      text: function(){
	*        res.send('hey');
	*      },
	*
	*      html: function(){
	*        res.send('<p>hey</p>');
	*      },
	*
	*      json: function(){
	*        res.send({ message: 'hey' });
	*      }
	*    });
	*
	* By default Express passes an `Error`
	* with a `.status` of 406 to `next(err)`
	* if a match is not made. If you provide
	* a `.default` callback it will be invoked
	* instead.
	*
	* @param {Object} obj
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.format = function(obj) {
		var req = this.req;
		var next = req.next;
		var keys = Object.keys(obj).filter(function(v) {
			return v !== "default";
		});
		var key = keys.length > 0 ? req.accepts(keys) : false;
		this.vary("Accept");
		if (key) {
			this.set("Content-Type", normalizeType(key).value);
			obj[key](req, this, next);
		} else if (obj.default) obj.default(req, this, next);
		else next(createError(406, { types: normalizeTypes(keys).map(function(o) {
			return o.value;
		}) }));
		return this;
	};
	/**
	* Set _Content-Disposition_ header to _attachment_ with optional `filename`.
	*
	* @param {String} filename
	* @return {ServerResponse}
	* @public
	*/
	res.attachment = function attachment(filename) {
		if (filename) this.type(extname(filename));
		this.set("Content-Disposition", contentDisposition(filename));
		return this;
	};
	/**
	* Append additional header `field` with value `val`.
	*
	* Example:
	*
	*    res.append('Link', ['<http://localhost/>', '<http://localhost:3000/>']);
	*    res.append('Set-Cookie', 'foo=bar; Path=/; HttpOnly');
	*    res.append('Warning', '199 Miscellaneous warning');
	*
	* @param {String} field
	* @param {String|Array} val
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.append = function append(field, val) {
		var prev = this.get(field);
		var value = val;
		if (prev) value = Array.isArray(prev) ? prev.concat(val) : Array.isArray(val) ? [prev].concat(val) : [prev, val];
		return this.set(field, value);
	};
	/**
	* Set header `field` to `val`, or pass
	* an object of header fields.
	*
	* Examples:
	*
	*    res.set('Foo', ['bar', 'baz']);
	*    res.set('Accept', 'application/json');
	*    res.set({ Accept: 'text/plain', 'X-API-Key': 'tobi' });
	*
	* Aliased as `res.header()`.
	*
	* When the set header is "Content-Type", the type is expanded to include
	* the charset if not present using `mime.contentType()`.
	*
	* @param {String|Object} field
	* @param {String|Array} val
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.set = res.header = function header(field, val) {
		if (arguments.length === 2) {
			var value = Array.isArray(val) ? val.map(String) : String(val);
			if (field.toLowerCase() === "content-type") {
				if (Array.isArray(value)) throw new TypeError("Content-Type cannot be set to an Array");
				value = mime.contentType(value);
			}
			this.setHeader(field, value);
		} else for (var key in field) this.set(key, field[key]);
		return this;
	};
	/**
	* Get value for header `field`.
	*
	* @param {String} field
	* @return {String}
	* @public
	*/
	res.get = function(field) {
		return this.getHeader(field);
	};
	/**
	* Clear cookie `name`.
	*
	* @param {String} name
	* @param {Object} [options]
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.clearCookie = function clearCookie(name, options) {
		const opts = {
			path: "/",
			...options,
			expires: /* @__PURE__ */ new Date(1)
		};
		delete opts.maxAge;
		return this.cookie(name, "", opts);
	};
	/**
	* Set cookie `name` to `value`, with the given `options`.
	*
	* Options:
	*
	*    - `maxAge`   max-age in milliseconds, converted to `expires`
	*    - `signed`   sign the cookie
	*    - `path`     defaults to "/"
	*
	* Examples:
	*
	*    // "Remember Me" for 15 minutes
	*    res.cookie('rememberme', '1', { expires: new Date(Date.now() + 900000), httpOnly: true });
	*
	*    // same as above
	*    res.cookie('rememberme', '1', { maxAge: 900000, httpOnly: true })
	*
	* @param {String} name
	* @param {String|Object} value
	* @param {Object} [options]
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.cookie = function(name, value, options) {
		var opts = { ...options };
		var secret = this.req.secret;
		var signed = opts.signed;
		if (signed && !secret) throw new Error("cookieParser(\"secret\") required for signed cookies");
		var val = typeof value === "object" ? "j:" + JSON.stringify(value) : String(value);
		if (signed) val = "s:" + sign(val, secret);
		if (opts.maxAge != null) {
			var maxAge = opts.maxAge - 0;
			if (!isNaN(maxAge)) {
				opts.expires = new Date(Date.now() + maxAge);
				opts.maxAge = Math.floor(maxAge / 1e3);
			}
		}
		if (opts.path == null) opts.path = "/";
		this.append("Set-Cookie", cookie.serialize(name, String(val), opts));
		return this;
	};
	/**
	* Set the location header to `url`.
	*
	* The given `url` can also be "back", which redirects
	* to the _Referrer_ or _Referer_ headers or "/".
	*
	* Examples:
	*
	*    res.location('/foo/bar').;
	*    res.location('http://example.com');
	*    res.location('../login');
	*
	* @param {String} url
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.location = function location(url) {
		return this.set("Location", encodeUrl(url));
	};
	/**
	* Redirect to the given `url` with optional response `status`
	* defaulting to 302.
	*
	* Examples:
	*
	*    res.redirect('/foo/bar');
	*    res.redirect('http://example.com');
	*    res.redirect(301, 'http://example.com');
	*    res.redirect('../login'); // /blog/post/1 -> /blog/login
	*
	* @public
	*/
	res.redirect = function redirect(url) {
		var address = url;
		var body;
		var status = 302;
		if (arguments.length === 2) {
			status = arguments[0];
			address = arguments[1];
		}
		if (!address) deprecate("Provide a url argument");
		if (typeof address !== "string") deprecate("Url must be a string");
		if (typeof status !== "number") deprecate("Status must be a number");
		address = this.location(address).get("Location");
		this.format({
			text: function() {
				body = statuses.message[status] + ". Redirecting to " + address;
			},
			html: function() {
				var u = escapeHtml(address);
				body = "<p>" + statuses.message[status] + ". Redirecting to " + u + "</p>";
			},
			default: function() {
				body = "";
			}
		});
		this.status(status);
		this.set("Content-Length", Buffer$1.byteLength(body));
		if (this.req.method === "HEAD") this.end();
		else this.end(body);
	};
	/**
	* Add `field` to Vary. If already present in the Vary set, then
	* this call is simply ignored.
	*
	* @param {Array|String} field
	* @return {ServerResponse} for chaining
	* @public
	*/
	res.vary = function(field) {
		vary(this, field);
		return this;
	};
	/**
	* Render `view` with the given `options` and optional callback `fn`.
	* When a callback function is given a response will _not_ be made
	* automatically, otherwise a response of _200_ and _text/html_ is given.
	*
	* Options:
	*
	*  - `cache`     boolean hinting to the engine it should cache
	*  - `filename`  filename of the view being rendered
	*
	* @public
	*/
	res.render = function render(view, options, callback) {
		var app = this.req.app;
		var done = callback;
		var opts = options || {};
		var req = this.req;
		var self = this;
		if (typeof options === "function") {
			done = options;
			opts = {};
		}
		opts._locals = self.locals;
		done = done || function(err, str) {
			if (err) return req.next(err);
			self.send(str);
		};
		app.render(view, opts, done);
	};
	function sendfile(res, file, options, callback) {
		var done = false;
		var streaming;
		function onaborted() {
			if (done) return;
			done = true;
			var err = /* @__PURE__ */ new Error("Request aborted");
			err.code = "ECONNABORTED";
			callback(err);
		}
		function ondirectory() {
			if (done) return;
			done = true;
			var err = /* @__PURE__ */ new Error("EISDIR, read");
			err.code = "EISDIR";
			callback(err);
		}
		function onerror(err) {
			if (done) return;
			done = true;
			callback(err);
		}
		function onend() {
			if (done) return;
			done = true;
			callback();
		}
		function onfile() {
			streaming = false;
		}
		function onfinish(err) {
			if (err && err.code === "ECONNRESET") return onaborted();
			if (err) return onerror(err);
			if (done) return;
			setImmediate(function() {
				if (streaming !== false && !done) {
					onaborted();
					return;
				}
				if (done) return;
				done = true;
				callback();
			});
		}
		function onstream() {
			streaming = true;
		}
		file.on("directory", ondirectory);
		file.on("end", onend);
		file.on("error", onerror);
		file.on("file", onfile);
		file.on("stream", onstream);
		onFinished(res, onfinish);
		if (options.headers) file.on("headers", function headers(res) {
			var obj = options.headers;
			var keys = Object.keys(obj);
			for (var i = 0; i < keys.length; i++) {
				var k = keys[i];
				res.setHeader(k, obj[k]);
			}
		});
		file.pipe(res);
	}
	/**
	* Stringify JSON, like JSON.stringify, but v8 optimized, with the
	* ability to escape characters that can trigger HTML sniffing.
	*
	* @param {*} value
	* @param {function} replacer
	* @param {number} spaces
	* @param {boolean} escape
	* @returns {string}
	* @private
	*/
	function stringify(value, replacer, spaces, escape) {
		var json = replacer || spaces ? JSON.stringify(value, replacer, spaces) : JSON.stringify(value);
		if (escape && typeof json === "string") json = json.replace(/[<>&]/g, function(c) {
			switch (c.charCodeAt(0)) {
				case 60: return "\\u003c";
				case 62: return "\\u003e";
				case 38: return "\\u0026";
				/* istanbul ignore next: unreachable default */
				default: return c;
			}
		});
		return json;
	}
}));
//#endregion
//#region ../node_modules/serve-static/index.js
/*!
* serve-static
* Copyright(c) 2010 Sencha Inc.
* Copyright(c) 2011 TJ Holowaychuk
* Copyright(c) 2014-2016 Douglas Christopher Wilson
* MIT Licensed
*/
var require_serve_static = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	* @private
	*/
	var encodeUrl = require_encodeurl();
	var escapeHtml = require_escape_html();
	var parseUrl = require_parseurl();
	var resolve = __require("path").resolve;
	var send = require_send();
	var url = __require("url");
	/**
	* Module exports.
	* @public
	*/
	module.exports = serveStatic;
	/**
	* @param {string} root
	* @param {object} [options]
	* @return {function}
	* @public
	*/
	function serveStatic(root, options) {
		if (!root) throw new TypeError("root path required");
		if (typeof root !== "string") throw new TypeError("root path must be a string");
		var opts = Object.create(options || null);
		var fallthrough = opts.fallthrough !== false;
		var redirect = opts.redirect !== false;
		var setHeaders = opts.setHeaders;
		if (setHeaders && typeof setHeaders !== "function") throw new TypeError("option setHeaders must be function");
		opts.maxage = opts.maxage || opts.maxAge || 0;
		opts.root = resolve(root);
		var onDirectory = redirect ? createRedirectDirectoryListener() : createNotFoundDirectoryListener();
		return function serveStatic(req, res, next) {
			if (req.method !== "GET" && req.method !== "HEAD") {
				if (fallthrough) return next();
				res.statusCode = 405;
				res.setHeader("Allow", "GET, HEAD");
				res.setHeader("Content-Length", "0");
				res.end();
				return;
			}
			var forwardError = !fallthrough;
			var originalUrl = parseUrl.original(req);
			var path = parseUrl(req).pathname;
			if (path === "/" && originalUrl.pathname.substr(-1) !== "/") path = "";
			var stream = send(req, path, opts);
			stream.on("directory", onDirectory);
			if (setHeaders) stream.on("headers", setHeaders);
			if (fallthrough) stream.on("file", function onFile() {
				forwardError = true;
			});
			stream.on("error", function error(err) {
				if (forwardError || !(err.statusCode < 500)) {
					next(err);
					return;
				}
				next();
			});
			stream.pipe(res);
		};
	}
	/**
	* Collapse all leading slashes into a single slash
	* @private
	*/
	function collapseLeadingSlashes(str) {
		for (var i = 0; i < str.length; i++) if (str.charCodeAt(i) !== 47) break;
		return i > 1 ? "/" + str.substr(i) : str;
	}
	/**
	* Create a minimal HTML document.
	*
	* @param {string} title
	* @param {string} body
	* @private
	*/
	function createHtmlDocument(title, body) {
		return "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>" + title + "</title>\n</head>\n<body>\n<pre>" + body + "</pre>\n</body>\n</html>\n";
	}
	/**
	* Create a directory listener that just 404s.
	* @private
	*/
	function createNotFoundDirectoryListener() {
		return function notFound() {
			this.error(404);
		};
	}
	/**
	* Create a directory listener that performs a redirect.
	* @private
	*/
	function createRedirectDirectoryListener() {
		return function redirect(res) {
			if (this.hasTrailingSlash()) {
				this.error(404);
				return;
			}
			var originalUrl = parseUrl.original(this.req);
			originalUrl.path = null;
			originalUrl.pathname = collapseLeadingSlashes(originalUrl.pathname + "/");
			var loc = encodeUrl(url.format(originalUrl));
			var doc = createHtmlDocument("Redirecting", "Redirecting to " + escapeHtml(loc));
			res.statusCode = 301;
			res.setHeader("Content-Type", "text/html; charset=UTF-8");
			res.setHeader("Content-Length", Buffer.byteLength(doc));
			res.setHeader("Content-Security-Policy", "default-src 'none'");
			res.setHeader("X-Content-Type-Options", "nosniff");
			res.setHeader("Location", loc);
			res.end(doc);
		};
	}
}));
//#endregion
//#region ../node_modules/express/lib/express.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_express$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module dependencies.
	*/
	var bodyParser = require_body_parser();
	var EventEmitter = __require("node:events").EventEmitter;
	var mixin = require_merge_descriptors();
	var proto = require_application();
	var Router = require_router();
	var req = require_request();
	var res = require_response();
	/**
	* Expose `createApplication()`.
	*/
	exports = module.exports = createApplication;
	/**
	* Create an express application.
	*
	* @return {Function}
	* @api public
	*/
	function createApplication() {
		var app = function(req, res, next) {
			app.handle(req, res, next);
		};
		mixin(app, EventEmitter.prototype, false);
		mixin(app, proto, false);
		app.request = Object.create(req, { app: {
			configurable: true,
			enumerable: true,
			writable: true,
			value: app
		} });
		app.response = Object.create(res, { app: {
			configurable: true,
			enumerable: true,
			writable: true,
			value: app
		} });
		app.init();
		return app;
	}
	/**
	* Expose the prototypes.
	*/
	exports.application = proto;
	exports.request = req;
	exports.response = res;
	/**
	* Expose constructors.
	*/
	exports.Route = Router.Route;
	exports.Router = Router;
	/**
	* Expose middleware
	*/
	exports.json = bodyParser.json;
	exports.raw = bodyParser.raw;
	exports.static = require_serve_static();
	exports.text = bodyParser.text;
	exports.urlencoded = bodyParser.urlencoded;
}));
//#endregion
//#region ../node_modules/express/index.js
/*!
* express
* Copyright(c) 2009-2013 TJ Holowaychuk
* Copyright(c) 2013 Roman Shtylman
* Copyright(c) 2014-2015 Douglas Christopher Wilson
* MIT Licensed
*/
var require_express = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	module.exports = require_express$1();
}));
//#endregion
export { require_express as t };
