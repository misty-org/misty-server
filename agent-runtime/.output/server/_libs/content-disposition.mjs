import { createRequire as __wkfCreateRequire } from "node:module";
if (typeof globalThis.require === "undefined") globalThis.require = __wkfCreateRequire(import.meta.url);
import { t as __commonJSMin } from "../_runtime.mjs";
//#region ../node_modules/content-disposition/index.js
/*!
* content-disposition
* Copyright(c) 2014-2017 Douglas Christopher Wilson
* MIT Licensed
*/
var require_content_disposition = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	/**
	* Module exports.
	* @public
	*/
	module.exports = contentDisposition;
	module.exports.parse = parse;
	/**
	* TextDecoder instance for UTF-8 decoding when decodeURIComponent fails due to invalid byte sequences.
	* @type {TextDecoder}
	* @private
	*/
	const utf8Decoder = new TextDecoder("utf-8");
	/**
	* RegExp to match non attr-char, *after* encodeURIComponent (i.e. not including "%")
	* @private
	*/
	var ENCODE_URL_ATTR_CHAR_REGEXP = /[\x00-\x20"'()*,/:;<=>?@[\\\]{}\x7f]/g;
	/**
	* RegExp to match non-latin1 characters.
	* @private
	*/
	var NON_LATIN1_REGEXP = /[^\x20-\x7e\xa0-\xff]/g;
	/**
	* RegExp to match quoted-pair in RFC 2616
	*
	* quoted-pair = "\" CHAR
	* CHAR        = <any US-ASCII character (octets 0 - 127)>
	* @private
	*/
	var QESC_REGEXP = /\\([\u0000-\u007f])/g;
	/**
	* RegExp to match chars that must be quoted-pair in RFC 2616
	* @private
	*/
	var QUOTE_REGEXP = /([\\"])/g;
	/**
	* RegExp for various RFC 2616 grammar
	*
	* parameter     = token "=" ( token | quoted-string )
	* token         = 1*<any CHAR except CTLs or separators>
	* separators    = "(" | ")" | "<" | ">" | "@"
	*               | "," | ";" | ":" | "\" | <">
	*               | "/" | "[" | "]" | "?" | "="
	*               | "{" | "}" | SP | HT
	* quoted-string = ( <"> *(qdtext | quoted-pair ) <"> )
	* qdtext        = <any TEXT except <">>
	* quoted-pair   = "\" CHAR
	* CHAR          = <any US-ASCII character (octets 0 - 127)>
	* TEXT          = <any OCTET except CTLs, but including LWS>
	* LWS           = [CRLF] 1*( SP | HT )
	* CRLF          = CR LF
	* CR            = <US-ASCII CR, carriage return (13)>
	* LF            = <US-ASCII LF, linefeed (10)>
	* SP            = <US-ASCII SP, space (32)>
	* HT            = <US-ASCII HT, horizontal-tab (9)>
	* CTL           = <any US-ASCII control character (octets 0 - 31) and DEL (127)>
	* OCTET         = <any 8-bit sequence of data>
	* @private
	*/
	var PARAM_REGEXP = /;[\x09\x20]*([!#$%&'*+.0-9A-Z^_`a-z|~-]+)[\x09\x20]*=[\x09\x20]*("(?:[\x20!\x23-\x5b\x5d-\x7e\x80-\xff]|\\[\x20-\x7e])*"|[!#$%&'*+.0-9A-Z^_`a-z|~-]+)[\x09\x20]*/g;
	var TEXT_REGEXP = /^[\x20-\x7e\x80-\xff]+$/;
	var TOKEN_REGEXP = /^[!#$%&'*+.0-9A-Z^_`a-z|~-]+$/;
	/**
	* RegExp for various RFC 5987 grammar
	*
	* ext-value     = charset  "'" [ language ] "'" value-chars
	* charset       = "UTF-8" / "ISO-8859-1" / mime-charset
	* mime-charset  = 1*mime-charsetc
	* mime-charsetc = ALPHA / DIGIT
	*               / "!" / "#" / "$" / "%" / "&"
	*               / "+" / "-" / "^" / "_" / "`"
	*               / "{" / "}" / "~"
	* language      = ( 2*3ALPHA [ extlang ] )
	*               / 4ALPHA
	*               / 5*8ALPHA
	* extlang       = *3( "-" 3ALPHA )
	* value-chars   = *( pct-encoded / attr-char )
	* pct-encoded   = "%" HEXDIG HEXDIG
	* attr-char     = ALPHA / DIGIT
	*               / "!" / "#" / "$" / "&" / "+" / "-" / "."
	*               / "^" / "_" / "`" / "|" / "~"
	* @private
	*/
	var EXT_VALUE_REGEXP = /^([A-Za-z0-9!#$%&+\-^_`{}~]+)'(?:[A-Za-z]{2,3}(?:-[A-Za-z]{3}){0,3}|[A-Za-z]{4,8}|)'((?:%[0-9A-Fa-f]{2}|[A-Za-z0-9!#$&+.^_`|~-])+)$/;
	/**
	* RegExp for various RFC 6266 grammar
	*
	* disposition-type = "inline" | "attachment" | disp-ext-type
	* disp-ext-type    = token
	* disposition-parm = filename-parm | disp-ext-parm
	* filename-parm    = "filename" "=" value
	*                  | "filename*" "=" ext-value
	* disp-ext-parm    = token "=" value
	*                  | ext-token "=" ext-value
	* ext-token        = <the characters in token, followed by "*">
	* @private
	*/
	var DISPOSITION_TYPE_REGEXP = /^([!#$%&'*+.0-9A-Z^_`a-z|~-]+)[\x09\x20]*(?:$|;)/;
	/**
	* Create an attachment Content-Disposition header.
	*
	* @param {string} [filename]
	* @param {object} [options]
	* @param {string} [options.type=attachment]
	* @param {string|boolean} [options.fallback=true]
	* @return {string}
	* @public
	*/
	function contentDisposition(filename, options) {
		var opts = options || {};
		return format(new ContentDisposition(opts.type || "attachment", createparams(filename, opts.fallback)));
	}
	/**
	* Create parameters object from filename and fallback.
	*
	* @param {string} [filename]
	* @param {string|boolean} [fallback=true]
	* @return {object}
	* @private
	*/
	function createparams(filename, fallback) {
		if (filename === void 0) return;
		var params = {};
		if (typeof filename !== "string") throw new TypeError("filename must be a string");
		if (fallback === void 0) fallback = true;
		if (typeof fallback !== "string" && typeof fallback !== "boolean") throw new TypeError("fallback must be a string or boolean");
		if (typeof fallback === "string" && NON_LATIN1_REGEXP.test(fallback)) throw new TypeError("fallback must be ISO-8859-1 string");
		var name = basename(filename);
		var isQuotedString = TEXT_REGEXP.test(name);
		var fallbackName = typeof fallback !== "string" ? fallback && getlatin1(name) : basename(fallback);
		var hasFallback = typeof fallbackName === "string" && fallbackName !== name;
		if (hasFallback || !isQuotedString || hasHexEscape(name)) params["filename*"] = name;
		if (isQuotedString || hasFallback) params.filename = hasFallback ? fallbackName : name;
		return params;
	}
	/**
	* Format object to Content-Disposition header.
	*
	* @param {object} obj
	* @param {string} obj.type
	* @param {object} [obj.parameters]
	* @return {string}
	* @private
	*/
	function format(obj) {
		var parameters = obj.parameters;
		var type = obj.type;
		if (!type || typeof type !== "string" || !TOKEN_REGEXP.test(type)) throw new TypeError("invalid type");
		var string = String(type).toLowerCase();
		if (parameters && typeof parameters === "object") {
			var param;
			var params = Object.keys(parameters).sort();
			for (var i = 0; i < params.length; i++) {
				param = params[i];
				var val = param.slice(-1) === "*" ? ustring(parameters[param]) : qstring(parameters[param]);
				string += "; " + param + "=" + val;
			}
		}
		return string;
	}
	/**
	* Decode a RFC 5987 field value (gracefully).
	*
	* @param {string} str
	* @return {string}
	* @private
	*/
	function decodefield(str) {
		const match = EXT_VALUE_REGEXP.exec(str);
		if (!match) throw new TypeError("invalid extended field value");
		const charset = match[1].toLowerCase();
		const encoded = match[2];
		switch (charset) {
			case "iso-8859-1": return getlatin1(decodeHexEscapes(encoded));
			case "utf-8":
			case "utf8": try {
				return decodeURIComponent(encoded);
			} catch {
				const binary = decodeHexEscapes(encoded);
				const bytes = new Uint8Array(binary.length);
				for (let idx = 0; idx < binary.length; idx++) bytes[idx] = binary.charCodeAt(idx);
				return utf8Decoder.decode(bytes);
			}
		}
		throw new TypeError("unsupported charset in extended field");
	}
	/**
	* Get ISO-8859-1 version of string.
	*
	* @param {string} val
	* @return {string}
	* @private
	*/
	function getlatin1(val) {
		return String(val).replace(NON_LATIN1_REGEXP, "?");
	}
	/**
	* Parse Content-Disposition header string.
	*
	* @param {string} string
	* @return {object}
	* @public
	*/
	function parse(string) {
		if (!string || typeof string !== "string") throw new TypeError("argument string is required");
		var match = DISPOSITION_TYPE_REGEXP.exec(string);
		if (!match) throw new TypeError("invalid type format");
		var index = match[0].length;
		var type = match[1].toLowerCase();
		var key;
		var names = [];
		var params = {};
		var value;
		index = PARAM_REGEXP.lastIndex = match[0].slice(-1) === ";" ? index - 1 : index;
		while (match = PARAM_REGEXP.exec(string)) {
			if (match.index !== index) throw new TypeError("invalid parameter format");
			index += match[0].length;
			key = match[1].toLowerCase();
			value = match[2];
			if (names.indexOf(key) !== -1) throw new TypeError("invalid duplicate parameter");
			names.push(key);
			if (key.indexOf("*") + 1 === key.length) {
				key = key.slice(0, -1);
				value = decodefield(value);
				params[key] = value;
				continue;
			}
			if (typeof params[key] === "string") continue;
			if (value[0] === "\"") value = value.slice(1, -1).replace(QESC_REGEXP, "$1");
			params[key] = value;
		}
		if (index !== -1 && index !== string.length) throw new TypeError("invalid parameter format");
		return new ContentDisposition(type, params);
	}
	/**
	* Percent encode a single character.
	*
	* @param {string} char
	* @return {string}
	* @private
	*/
	function pencode(char) {
		return "%" + String(char).charCodeAt(0).toString(16).toUpperCase();
	}
	/**
	* Quote a string for HTTP.
	*
	* @param {string} val
	* @return {string}
	* @private
	*/
	function qstring(val) {
		return "\"" + String(val).replace(QUOTE_REGEXP, "\\$1") + "\"";
	}
	/**
	* Encode a Unicode string for HTTP (RFC 5987).
	*
	* @param {string} val
	* @return {string}
	* @private
	*/
	function ustring(val) {
		return "UTF-8''" + encodeURIComponent(String(val)).replace(ENCODE_URL_ATTR_CHAR_REGEXP, pencode);
	}
	/**
	* Class for parsed Content-Disposition header for v8 optimization
	*
	* @public
	* @param {string} type
	* @param {object} parameters
	* @constructor
	*/
	function ContentDisposition(type, parameters) {
		this.type = type;
		this.parameters = parameters;
	}
	/**
	* Return the last portion of a path
	*
	* @param {string} path
	* @returns {string}
	*/
	function basename(path) {
		const normalized = path.replaceAll("\\", "/");
		let end = normalized.length;
		while (end > 0 && normalized[end - 1] === "/") end--;
		if (end === 0) return "";
		let start = end - 1;
		while (start >= 0 && normalized[start] !== "/") start--;
		return normalized.slice(start + 1, end);
	}
	/**
	* Check if a character is a hex digit [0-9A-Fa-f]
	*
	* @param {string} char
	* @return {boolean}
	* @private
	*/
	function isHexDigit(char) {
		const code = char.charCodeAt(0);
		return code >= 48 && code <= 57 || code >= 65 && code <= 70 || code >= 97 && code <= 102;
	}
	/**
	* Check if a string contains percent encoding escapes.
	*
	* @param {string} str
	* @return {boolean}
	* @private
	*/
	function hasHexEscape(str) {
		const maxIndex = str.length - 3;
		let lastIndex = -1;
		while ((lastIndex = str.indexOf("%", lastIndex + 1)) !== -1 && lastIndex <= maxIndex) if (isHexDigit(str[lastIndex + 1]) && isHexDigit(str[lastIndex + 2])) return true;
		return false;
	}
	/**
	* Decode hex escapes in a string (e.g., %20 -> space)
	*
	* @param {string} str
	* @return {string}
	* @private
	*/
	function decodeHexEscapes(str) {
		const firstEscape = str.indexOf("%");
		if (firstEscape === -1) return str;
		let result = str.slice(0, firstEscape);
		for (let idx = firstEscape; idx < str.length; idx++) if (str[idx] === "%" && idx + 2 < str.length && isHexDigit(str[idx + 1]) && isHexDigit(str[idx + 2])) {
			result += String.fromCharCode(Number.parseInt(str[idx + 1] + str[idx + 2], 16));
			idx += 2;
		} else result += str[idx];
		return result;
	}
}));
//#endregion
export { require_content_disposition as t };
