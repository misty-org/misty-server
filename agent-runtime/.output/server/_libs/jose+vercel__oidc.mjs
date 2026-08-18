import { createRequire as __wkfCreateRequire } from "node:module";
if (typeof globalThis.require === "undefined") globalThis.require = __wkfCreateRequire(import.meta.url);
import { i as __toESM, r as __require, t as __commonJSMin } from "../_runtime.mjs";
import { t as require_dist$2 } from "./@vercel/cli-exec+[...].mjs";
import { t as require_dist$3 } from "./@vercel/cli-config+[...].mjs";
import { n as require_token_util$2, r as require_token_error$2 } from "./@ai-sdk/gateway+[...].mjs";
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/get-context.js
var require_get_context$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var get_context_exports = {};
	__export(get_context_exports, {
		SYMBOL_FOR_REQ_CONTEXT: () => SYMBOL_FOR_REQ_CONTEXT,
		getContext: () => getContext
	});
	module.exports = __toCommonJS(get_context_exports);
	const SYMBOL_FOR_REQ_CONTEXT = Symbol.for("@vercel/request-context");
	function getContext() {
		return globalThis[SYMBOL_FOR_REQ_CONTEXT]?.get?.() ?? {};
	}
	0 && (module.exports = {
		SYMBOL_FOR_REQ_CONTEXT,
		getContext
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/token-error.js
var require_token_error$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_error_exports = {};
	__export(token_error_exports, { VercelOidcTokenError: () => VercelOidcTokenError });
	module.exports = __toCommonJS(token_error_exports);
	var VercelOidcTokenError = class extends Error {
		constructor(message, cause) {
			super(message);
			this.name = "VercelOidcTokenError";
			this.cause = cause;
		}
		toString() {
			if (this.cause) return `${this.name}: ${this.message}: ${this.cause}`;
			return `${this.name}: ${this.message}`;
		}
	};
	0 && (module.exports = { VercelOidcTokenError });
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/get-vercel-oidc-token.js
var require_get_vercel_oidc_token = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var get_vercel_oidc_token_exports = {};
	__export(get_vercel_oidc_token_exports, {
		getVercelOidcToken: () => getVercelOidcToken,
		getVercelOidcTokenSync: () => getVercelOidcTokenSync
	});
	module.exports = __toCommonJS(get_vercel_oidc_token_exports);
	var import_get_context = require_get_context$1();
	var import_token_error = require_token_error$1();
	async function getVercelOidcToken(options) {
		let token = "";
		let err;
		try {
			token = getVercelOidcTokenSync();
		} catch (error) {
			err = error;
		}
		try {
			const [{ getTokenPayload, isExpired }, { refreshToken }] = await Promise.all([await Promise.resolve().then(() => /* @__PURE__ */ __toESM(require_token_util$1())), await Promise.resolve().then(() => /* @__PURE__ */ __toESM(require_token()))]);
			if (!token || isExpired(getTokenPayload(token), options?.expirationBufferMs)) {
				await refreshToken(options);
				token = getVercelOidcTokenSync();
			}
		} catch (error) {
			let message = err instanceof Error ? err.message : "";
			if (error instanceof Error) message = `${message}
${error.message}`;
			if (message) throw new import_token_error.VercelOidcTokenError(message);
			throw error;
		}
		return token;
	}
	function getVercelOidcTokenSync() {
		const token = (0, import_get_context.getContext)().headers?.["x-vercel-oidc-token"] ?? process.env.VERCEL_OIDC_TOKEN;
		if (!token) throw new Error(`The 'x-vercel-oidc-token' header is missing from the request. Do you have the OIDC option enabled in the Vercel project settings?`);
		return token;
	}
	0 && (module.exports = {
		getVercelOidcToken,
		getVercelOidcTokenSync
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/auth-errors.js
var require_auth_errors$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var auth_errors_exports = {};
	__export(auth_errors_exports, {
		AccessTokenMissingError: () => AccessTokenMissingError,
		RefreshAccessTokenFailedError: () => RefreshAccessTokenFailedError
	});
	module.exports = __toCommonJS(auth_errors_exports);
	var AccessTokenMissingError = class extends Error {
		constructor() {
			super("No authentication found. Please log in with the Vercel CLI (vercel login).");
			this.name = "AccessTokenMissingError";
		}
	};
	var RefreshAccessTokenFailedError = class extends Error {
		constructor(cause) {
			super("Failed to refresh authentication token.", { cause });
			this.name = "RefreshAccessTokenFailedError";
		}
	};
	0 && (module.exports = {
		AccessTokenMissingError,
		RefreshAccessTokenFailedError
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/token-io.js
var require_token_io$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __create = Object.create;
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __getProtoOf = Object.getPrototypeOf;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
		value: mod,
		enumerable: true
	}) : target, mod));
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_io_exports = {};
	__export(token_io_exports, {
		findRootDir: () => findRootDir,
		getUserDataDir: () => getUserDataDir
	});
	module.exports = __toCommonJS(token_io_exports);
	var import_path$1 = __toESM(__require("path"));
	var import_fs$1 = __toESM(__require("fs"));
	var import_os$3 = __toESM(__require("os"));
	var import_token_error = require_token_error$1();
	function findRootDir() {
		try {
			let dir = process.cwd();
			while (dir !== import_path$1.default.dirname(dir)) {
				const pkgPath = import_path$1.default.join(dir, ".vercel");
				if (import_fs$1.default.existsSync(pkgPath)) return dir;
				dir = import_path$1.default.dirname(dir);
			}
		} catch (e) {
			throw new import_token_error.VercelOidcTokenError("Token refresh only supported in node server environments");
		}
		return null;
	}
	function getUserDataDir() {
		if (process.env.XDG_DATA_HOME) return process.env.XDG_DATA_HOME;
		switch (import_os$3.default.platform()) {
			case "darwin": return import_path$1.default.join(import_os$3.default.homedir(), "Library/Application Support");
			case "linux": return import_path$1.default.join(import_os$3.default.homedir(), ".local/share");
			case "win32":
				if (process.env.LOCALAPPDATA) return process.env.LOCALAPPDATA;
				return null;
			default: return null;
		}
	}
	0 && (module.exports = {
		findRootDir,
		getUserDataDir
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/auth-config.js
var require_auth_config = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __create = Object.create;
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __getProtoOf = Object.getPrototypeOf;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
		value: mod,
		enumerable: true
	}) : target, mod));
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var auth_config_exports = {};
	__export(auth_config_exports, {
		isValidAccessToken: () => isValidAccessToken,
		readAuthConfig: () => readAuthConfig,
		writeAuthConfig: () => writeAuthConfig
	});
	module.exports = __toCommonJS(auth_config_exports);
	var fs$2 = __toESM(__require("fs"));
	var path$2 = __toESM(__require("path"));
	var import_token_util = require_token_util$1();
	function getAuthConfigPath() {
		const dataDir = (0, import_token_util.getVercelDataDir)();
		if (!dataDir) throw new Error(`Unable to find Vercel CLI data directory. Your platform: ${process.platform}. Supported: darwin, linux, win32.`);
		return path$2.join(dataDir, "auth.json");
	}
	function readAuthConfig() {
		try {
			const authPath = getAuthConfigPath();
			if (!fs$2.existsSync(authPath)) return null;
			const content = fs$2.readFileSync(authPath, "utf8");
			if (!content) return null;
			return JSON.parse(content);
		} catch (error) {
			return null;
		}
	}
	function writeAuthConfig(config) {
		const authPath = getAuthConfigPath();
		const authDir = path$2.dirname(authPath);
		if (!fs$2.existsSync(authDir)) fs$2.mkdirSync(authDir, {
			mode: 504,
			recursive: true
		});
		fs$2.writeFileSync(authPath, JSON.stringify(config, null, 2), { mode: 384 });
	}
	function isValidAccessToken(authConfig, expirationBufferMs = 0) {
		if (!authConfig.token) return false;
		if (typeof authConfig.expiresAt !== "number") return true;
		const nowInSeconds = Math.floor(Date.now() / 1e3);
		const bufferInSeconds = expirationBufferMs / 1e3;
		return authConfig.expiresAt >= nowInSeconds + bufferInSeconds;
	}
	0 && (module.exports = {
		isValidAccessToken,
		readAuthConfig,
		writeAuthConfig
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/oauth.js
var require_oauth$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var oauth_exports = {};
	__export(oauth_exports, {
		processTokenResponse: () => processTokenResponse,
		refreshTokenRequest: () => refreshTokenRequest
	});
	module.exports = __toCommonJS(oauth_exports);
	var import_os$2 = __require("os");
	const VERCEL_ISSUER = "https://vercel.com";
	const VERCEL_CLI_CLIENT_ID = "cl_HYyOPBNtFMfHhaUn9L4QPfTZz6TP47bp";
	const userAgent = `@vercel/oidc node-${process.version} ${(0, import_os$2.platform)()} (${(0, import_os$2.arch)()}) ${(0, import_os$2.hostname)()}`;
	let _tokenEndpoint = null;
	async function getTokenEndpoint() {
		if (_tokenEndpoint) return _tokenEndpoint;
		const response = await fetch(`${VERCEL_ISSUER}/.well-known/openid-configuration`, { headers: { "user-agent": userAgent } });
		if (!response.ok) throw new Error("Failed to discover OAuth endpoints");
		const metadata = await response.json();
		if (!metadata || typeof metadata.token_endpoint !== "string") throw new Error("Invalid OAuth discovery response");
		const endpoint = metadata.token_endpoint;
		_tokenEndpoint = endpoint;
		return endpoint;
	}
	async function refreshTokenRequest(options) {
		const tokenEndpoint = await getTokenEndpoint();
		return await fetch(tokenEndpoint, {
			method: "POST",
			headers: {
				"Content-Type": "application/x-www-form-urlencoded",
				"user-agent": userAgent
			},
			body: new URLSearchParams({
				client_id: VERCEL_CLI_CLIENT_ID,
				grant_type: "refresh_token",
				...options
			})
		});
	}
	async function processTokenResponse(response) {
		const json = await response.json();
		if (!response.ok) {
			const errorMsg = typeof json === "object" && json && "error" in json ? String(json.error) : "Token refresh failed";
			return [new Error(errorMsg)];
		}
		if (typeof json !== "object" || json === null) return [/* @__PURE__ */ new Error("Invalid token response")];
		if (typeof json.access_token !== "string") return [/* @__PURE__ */ new Error("Missing access_token in response")];
		if (json.token_type !== "Bearer") return [/* @__PURE__ */ new Error("Invalid token_type in response")];
		if (typeof json.expires_in !== "number") return [/* @__PURE__ */ new Error("Missing expires_in in response")];
		return [null, json];
	}
	0 && (module.exports = {
		processTokenResponse,
		refreshTokenRequest
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/token-util.js
var require_token_util$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __create = Object.create;
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __getProtoOf = Object.getPrototypeOf;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
		value: mod,
		enumerable: true
	}) : target, mod));
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_util_exports = {};
	__export(token_util_exports, {
		assertVercelOidcTokenResponse: () => assertVercelOidcTokenResponse,
		findProjectInfo: () => findProjectInfo,
		getTokenPayload: () => getTokenPayload,
		getVercelDataDir: () => getVercelDataDir,
		getVercelOidcToken: () => getVercelOidcToken,
		getVercelToken: () => getVercelToken,
		isExpired: () => isExpired,
		loadToken: () => loadToken,
		saveToken: () => saveToken
	});
	module.exports = __toCommonJS(token_util_exports);
	var path$1 = __toESM(__require("path"));
	var fs$1 = __toESM(__require("fs"));
	var import_token_error = require_token_error$1();
	var import_token_io = require_token_io$1();
	var import_auth_config = require_auth_config();
	var import_oauth = require_oauth$1();
	var import_auth_errors = require_auth_errors$1();
	function getVercelDataDir() {
		const vercelFolder = "com.vercel.cli";
		const dataDir = (0, import_token_io.getUserDataDir)();
		if (!dataDir) return null;
		return path$1.join(dataDir, vercelFolder);
	}
	async function getVercelToken(options) {
		const authConfig = (0, import_auth_config.readAuthConfig)();
		if (!authConfig?.token) throw new import_auth_errors.AccessTokenMissingError();
		if ((0, import_auth_config.isValidAccessToken)(authConfig, options?.expirationBufferMs)) return authConfig.token;
		if (!authConfig.refreshToken) {
			(0, import_auth_config.writeAuthConfig)({});
			throw new import_auth_errors.RefreshAccessTokenFailedError("No refresh token available");
		}
		try {
			const tokenResponse = await (0, import_oauth.refreshTokenRequest)({ refresh_token: authConfig.refreshToken });
			const [tokensError, tokens] = await (0, import_oauth.processTokenResponse)(tokenResponse);
			if (tokensError || !tokens) {
				(0, import_auth_config.writeAuthConfig)({});
				throw new import_auth_errors.RefreshAccessTokenFailedError(tokensError);
			}
			const updatedConfig = {
				token: tokens.access_token,
				expiresAt: Math.floor(Date.now() / 1e3) + tokens.expires_in
			};
			if (tokens.refresh_token) updatedConfig.refreshToken = tokens.refresh_token;
			(0, import_auth_config.writeAuthConfig)(updatedConfig);
			return updatedConfig.token;
		} catch (error) {
			(0, import_auth_config.writeAuthConfig)({});
			if (error instanceof import_auth_errors.AccessTokenMissingError || error instanceof import_auth_errors.RefreshAccessTokenFailedError) throw error;
			throw new import_auth_errors.RefreshAccessTokenFailedError(error);
		}
	}
	async function getVercelOidcToken(authToken, projectId, teamId) {
		const url = `https://api.vercel.com/v1/projects/${projectId}/token?source=vercel-oidc-refresh${teamId ? `&teamId=${teamId}` : ""}`;
		const res = await fetch(url, {
			method: "POST",
			headers: { Authorization: `Bearer ${authToken}` }
		});
		if (!res.ok) throw new import_token_error.VercelOidcTokenError(`Failed to refresh OIDC token: ${res.statusText}`);
		const tokenRes = await res.json();
		assertVercelOidcTokenResponse(tokenRes);
		return tokenRes;
	}
	function assertVercelOidcTokenResponse(res) {
		if (!res || typeof res !== "object") throw new TypeError("Vercel OIDC token is malformed. Expected an object. Please run `vc env pull` and try again");
		if (!("token" in res) || typeof res.token !== "string") throw new TypeError("Vercel OIDC token is malformed. Expected a string-valued token property. Please run `vc env pull` and try again");
	}
	function findProjectInfo() {
		const dir = (0, import_token_io.findRootDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find project root directory. Have you linked your project with `vc link?`");
		const prjPath = path$1.join(dir, ".vercel", "project.json");
		if (!fs$1.existsSync(prjPath)) throw new import_token_error.VercelOidcTokenError("project.json not found, have you linked your project with `vc link?`");
		const prj = JSON.parse(fs$1.readFileSync(prjPath, "utf8"));
		if (typeof prj.projectId !== "string" && typeof prj.orgId !== "string") throw new TypeError("Expected a string-valued projectId property. Try running `vc link` to re-link your project.");
		return {
			projectId: prj.projectId,
			teamId: prj.orgId
		};
	}
	function saveToken(token, projectId) {
		const dir = (0, import_token_io.getUserDataDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find user data directory. Please reach out to Vercel support.");
		const tokenPath = path$1.join(dir, "com.vercel.token", `${projectId}.json`);
		const tokenJson = JSON.stringify(token);
		fs$1.mkdirSync(path$1.dirname(tokenPath), {
			mode: 504,
			recursive: true
		});
		fs$1.writeFileSync(tokenPath, tokenJson);
		fs$1.chmodSync(tokenPath, 432);
	}
	function loadToken(projectId) {
		const dir = (0, import_token_io.getUserDataDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find user data directory. Please reach out to Vercel support.");
		const tokenPath = path$1.join(dir, "com.vercel.token", `${projectId}.json`);
		if (!fs$1.existsSync(tokenPath)) return null;
		const token = JSON.parse(fs$1.readFileSync(tokenPath, "utf8"));
		assertVercelOidcTokenResponse(token);
		return token;
	}
	function getTokenPayload(token) {
		const tokenParts = token.split(".");
		if (tokenParts.length !== 3) throw new import_token_error.VercelOidcTokenError("Invalid token. Please run `vc env pull` and try again");
		const base64 = tokenParts[1].replace(/-/g, "+").replace(/_/g, "/");
		const padded = base64.padEnd(base64.length + (4 - base64.length % 4) % 4, "=");
		return JSON.parse(Buffer.from(padded, "base64").toString("utf8"));
	}
	function isExpired(token, bufferMs = 0) {
		return token.exp * 1e3 < Date.now() + bufferMs;
	}
	0 && (module.exports = {
		assertVercelOidcTokenResponse,
		findProjectInfo,
		getTokenPayload,
		getVercelDataDir,
		getVercelOidcToken,
		getVercelToken,
		isExpired,
		loadToken,
		saveToken
	});
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/index.js
var require_dist$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var src_exports = {};
	__export(src_exports, {
		AccessTokenMissingError: () => import_auth_errors.AccessTokenMissingError,
		RefreshAccessTokenFailedError: () => import_auth_errors.RefreshAccessTokenFailedError,
		getContext: () => import_get_context.getContext,
		getVercelOidcToken: () => import_get_vercel_oidc_token.getVercelOidcToken,
		getVercelOidcTokenSync: () => import_get_vercel_oidc_token.getVercelOidcTokenSync,
		getVercelToken: () => import_token_util.getVercelToken
	});
	module.exports = __toCommonJS(src_exports);
	var import_get_vercel_oidc_token = require_get_vercel_oidc_token();
	var import_get_context = require_get_context$1();
	var import_auth_errors = require_auth_errors$1();
	var import_token_util = require_token_util$1();
	0 && (module.exports = {
		AccessTokenMissingError,
		RefreshAccessTokenFailedError,
		getContext,
		getVercelOidcToken,
		getVercelOidcTokenSync,
		getVercelToken
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/version.js
var require_version = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var version_exports = {};
	__export(version_exports, { version: () => version });
	module.exports = __toCommonJS(version_exports);
	const version = "3.8.4";
	0 && (module.exports = { version });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/exchange-vercel-oidc-token.js
var require_exchange_vercel_oidc_token = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var exchange_vercel_oidc_token_exports = {};
	__export(exchange_vercel_oidc_token_exports, { exchangeVercelOidcToken: () => exchangeVercelOidcToken });
	module.exports = __toCommonJS(exchange_vercel_oidc_token_exports);
	var import_version = require_version();
	var TokenCache = class {
		constructor(maxEntries) {
			this.maxEntries = maxEntries;
			this.entries = /* @__PURE__ */ new Map();
		}
		/**
		* Returns a cached token for the key when present and unexpired, refreshing
		* its recency for LRU eviction. Expired entries are removed on access.
		*/
		get(key) {
			const entry = this.entries.get(key);
			if (entry === void 0) return;
			if (entry.expiresAt <= Date.now()) {
				this.entries.delete(key);
				return;
			}
			this.entries.delete(key);
			this.entries.set(key, entry);
			return entry.token;
		}
		/**
		* Stores a token under the key and evicts the least-recently-used entries
		* once the cache exceeds its size limit.
		*/
		set({ key, token, expiresAt }) {
			this.entries.delete(key);
			this.entries.set(key, {
				token,
				expiresAt
			});
			while (this.entries.size > this.maxEntries) {
				const oldest = this.entries.keys().next().value;
				if (oldest === void 0) break;
				this.entries.delete(oldest);
			}
		}
	};
	const tokenCache = new TokenCache(1e3);
	async function getCacheKey(options) {
		const input = JSON.stringify([
			options.token,
			options.audience,
			options.jti
		]);
		const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(input));
		return Array.from(new Uint8Array(digest)).map((byte) => byte.toString(16).padStart(2, "0")).join("");
	}
	async function exchangeVercelOidcToken(options) {
		const cacheKey = await getCacheKey(options);
		if (!options.skipCache) {
			const cached = tokenCache.get(cacheKey);
			if (cached !== void 0) return cached;
		}
		const response = await fetch("https://oidc.vercel.com/~token", {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Accept: "application/json",
				"User-Agent": `@vercel/oidc@${import_version.version}`
			},
			body: JSON.stringify({
				token: options.token,
				aud: options.audience,
				...options.jti ? { jti: options.jti } : void 0
			})
		});
		if (!response.ok) throw new Error(`Failed to exchange token: ${await readErrorMessage(response)}`);
		let data;
		try {
			data = await response.json();
		} catch (_error) {
			throw new Error("Failed to exchange token: response was not valid JSON");
		}
		if (!data || typeof data !== "object" || !("token" in data) || typeof data.token !== "string") throw new Error("Failed to exchange token: response did not contain a token");
		const { token } = data;
		const expiry = "expiry" in data && typeof data.expiry === "number" ? data.expiry : void 0;
		if (expiry !== void 0) {
			const expiresAt = expiry * 1e3;
			if (expiresAt > Date.now()) tokenCache.set({
				key: cacheKey,
				token,
				expiresAt
			});
		}
		return token;
	}
	async function readErrorMessage(response) {
		try {
			const data = await response.json();
			if (data && typeof data === "object" && "error" in data && typeof data.error === "string") return data.error;
		} catch (_error) {}
		return response.statusText || `HTTP ${response.status}`;
	}
	0 && (module.exports = { exchangeVercelOidcToken });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/get-context.js
var require_get_context = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var get_context_exports = {};
	__export(get_context_exports, {
		SYMBOL_FOR_REQ_CONTEXT: () => SYMBOL_FOR_REQ_CONTEXT,
		getContext: () => getContext
	});
	module.exports = __toCommonJS(get_context_exports);
	const SYMBOL_FOR_REQ_CONTEXT = Symbol.for("@vercel/request-context");
	function getContext() {
		return globalThis[SYMBOL_FOR_REQ_CONTEXT]?.get?.() ?? {};
	}
	0 && (module.exports = {
		SYMBOL_FOR_REQ_CONTEXT,
		getContext
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/get-vercel-oidc-token-sync.js
var require_get_vercel_oidc_token_sync = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var get_vercel_oidc_token_sync_exports = {};
	__export(get_vercel_oidc_token_sync_exports, { getVercelOidcTokenSync: () => getVercelOidcTokenSync });
	module.exports = __toCommonJS(get_vercel_oidc_token_sync_exports);
	var import_get_context = require_get_context();
	function getVercelOidcTokenSync() {
		const token = (0, import_get_context.getContext)().headers?.["x-vercel-oidc-token"] ?? process.env.VERCEL_OIDC_TOKEN;
		if (!token) throw new Error(`The 'x-vercel-oidc-token' header is missing from the request.`);
		return token;
	}
	0 && (module.exports = { getVercelOidcTokenSync });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/token-error.js
var require_token_error = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_error_exports = {};
	__export(token_error_exports, { VercelOidcTokenError: () => VercelOidcTokenError });
	module.exports = __toCommonJS(token_error_exports);
	var VercelOidcTokenError = class extends Error {
		constructor(message, cause) {
			super(message);
			this.name = "VercelOidcTokenError";
			this.cause = cause;
		}
		toString() {
			if (this.cause) return `${this.name}: ${this.message}: ${this.cause}`;
			return `${this.name}: ${this.message}`;
		}
	};
	0 && (module.exports = { VercelOidcTokenError });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/get-vercel-oidc-token-with-refresh.js
var require_get_vercel_oidc_token_with_refresh = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var get_vercel_oidc_token_with_refresh_exports = {};
	__export(get_vercel_oidc_token_with_refresh_exports, { getVercelOidcToken: () => getVercelOidcToken });
	module.exports = __toCommonJS(get_vercel_oidc_token_with_refresh_exports);
	var import_exchange_vercel_oidc_token = require_exchange_vercel_oidc_token();
	var import_get_vercel_oidc_token_sync = require_get_vercel_oidc_token_sync();
	var import_token_error = require_token_error();
	async function getVercelOidcToken(options) {
		let token = "";
		let err;
		try {
			token = (0, import_get_vercel_oidc_token_sync.getVercelOidcTokenSync)();
		} catch (error) {
			err = error;
		}
		try {
			const [{ getTokenPayload, isExpired }, { refreshToken }] = await Promise.all([await Promise.resolve().then(() => /* @__PURE__ */ __toESM(require_token_util())), await Promise.resolve().then(() => /* @__PURE__ */ __toESM(require_token$1()))]);
			if (!token || isExpired(getTokenPayload(token), options?.expirationBufferMs)) {
				await refreshToken(options);
				token = (0, import_get_vercel_oidc_token_sync.getVercelOidcTokenSync)();
			}
		} catch (error) {
			let message = err instanceof Error ? err.message : "";
			if (error instanceof Error) message = `${message}
${error.message}`;
			if (message) throw new import_token_error.VercelOidcTokenError(message);
			throw error;
		}
		if (options?.audience) token = await (0, import_exchange_vercel_oidc_token.exchangeVercelOidcToken)({
			token,
			audience: options.audience,
			jti: options.jti,
			skipCache: options.skipCache
		});
		return token;
	}
	0 && (module.exports = { getVercelOidcToken });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/digest.js
var require_digest = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$17 = __require("node:crypto");
	const digest = (algorithm, data) => (0, node_crypto_1$17.createHash)(algorithm).update(data).digest();
	exports.default = digest;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/buffer_utils.js
var require_buffer_utils = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decoder = exports.encoder = void 0;
	exports.concat = concat;
	exports.p2s = p2s;
	exports.uint64be = uint64be;
	exports.uint32be = uint32be;
	exports.lengthAndInput = lengthAndInput;
	exports.concatKdf = concatKdf;
	const digest_js_1 = require_digest();
	exports.encoder = new TextEncoder();
	exports.decoder = new TextDecoder();
	const MAX_INT32 = 2 ** 32;
	function concat(...buffers) {
		const size = buffers.reduce((acc, { length }) => acc + length, 0);
		const buf = new Uint8Array(size);
		let i = 0;
		for (const buffer of buffers) {
			buf.set(buffer, i);
			i += buffer.length;
		}
		return buf;
	}
	function p2s(alg, p2sInput) {
		return concat(exports.encoder.encode(alg), new Uint8Array([0]), p2sInput);
	}
	function writeUInt32BE(buf, value, offset) {
		if (value < 0 || value >= MAX_INT32) throw new RangeError(`value must be >= 0 and <= ${MAX_INT32 - 1}. Received ${value}`);
		buf.set([
			value >>> 24,
			value >>> 16,
			value >>> 8,
			value & 255
		], offset);
	}
	function uint64be(value) {
		const high = Math.floor(value / MAX_INT32);
		const low = value % MAX_INT32;
		const buf = /* @__PURE__ */ new Uint8Array(8);
		writeUInt32BE(buf, high, 0);
		writeUInt32BE(buf, low, 4);
		return buf;
	}
	function uint32be(value) {
		const buf = /* @__PURE__ */ new Uint8Array(4);
		writeUInt32BE(buf, value);
		return buf;
	}
	function lengthAndInput(input) {
		return concat(uint32be(input.length), input);
	}
	async function concatKdf(secret, bits, value) {
		const iterations = Math.ceil((bits >> 3) / 32);
		const res = new Uint8Array(iterations * 32);
		for (let iter = 0; iter < iterations; iter++) {
			const buf = new Uint8Array(4 + secret.length + value.length);
			buf.set(uint32be(iter + 1));
			buf.set(secret, 4);
			buf.set(value, 4 + secret.length);
			res.set(await (0, digest_js_1.default)("sha256", buf), iter * 32);
		}
		return res.slice(0, bits >> 3);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/base64url.js
var require_base64url$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decode = exports.encode = exports.encodeBase64 = exports.decodeBase64 = void 0;
	const node_buffer_1$2 = __require("node:buffer");
	const buffer_utils_js_1 = require_buffer_utils();
	function normalize(input) {
		let encoded = input;
		if (encoded instanceof Uint8Array) encoded = buffer_utils_js_1.decoder.decode(encoded);
		return encoded;
	}
	const encode = (input) => node_buffer_1$2.Buffer.from(input).toString("base64url");
	exports.encode = encode;
	const decodeBase64 = (input) => new Uint8Array(node_buffer_1$2.Buffer.from(input, "base64"));
	exports.decodeBase64 = decodeBase64;
	const encodeBase64 = (input) => node_buffer_1$2.Buffer.from(input).toString("base64");
	exports.encodeBase64 = encodeBase64;
	const decode = (input) => new Uint8Array(node_buffer_1$2.Buffer.from(normalize(input), "base64url"));
	exports.decode = decode;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/util/errors.js
var require_errors = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.JWSSignatureVerificationFailed = exports.JWKSTimeout = exports.JWKSMultipleMatchingKeys = exports.JWKSNoMatchingKey = exports.JWKSInvalid = exports.JWKInvalid = exports.JWTInvalid = exports.JWSInvalid = exports.JWEInvalid = exports.JWEDecryptionFailed = exports.JOSENotSupported = exports.JOSEAlgNotAllowed = exports.JWTExpired = exports.JWTClaimValidationFailed = exports.JOSEError = void 0;
	var JOSEError = class extends Error {
		static code = "ERR_JOSE_GENERIC";
		code = "ERR_JOSE_GENERIC";
		constructor(message, options) {
			super(message, options);
			this.name = this.constructor.name;
			Error.captureStackTrace?.(this, this.constructor);
		}
	};
	exports.JOSEError = JOSEError;
	var JWTClaimValidationFailed = class extends JOSEError {
		static code = "ERR_JWT_CLAIM_VALIDATION_FAILED";
		code = "ERR_JWT_CLAIM_VALIDATION_FAILED";
		claim;
		reason;
		payload;
		constructor(message, payload, claim = "unspecified", reason = "unspecified") {
			super(message, { cause: {
				claim,
				reason,
				payload
			} });
			this.claim = claim;
			this.reason = reason;
			this.payload = payload;
		}
	};
	exports.JWTClaimValidationFailed = JWTClaimValidationFailed;
	var JWTExpired = class extends JOSEError {
		static code = "ERR_JWT_EXPIRED";
		code = "ERR_JWT_EXPIRED";
		claim;
		reason;
		payload;
		constructor(message, payload, claim = "unspecified", reason = "unspecified") {
			super(message, { cause: {
				claim,
				reason,
				payload
			} });
			this.claim = claim;
			this.reason = reason;
			this.payload = payload;
		}
	};
	exports.JWTExpired = JWTExpired;
	var JOSEAlgNotAllowed = class extends JOSEError {
		static code = "ERR_JOSE_ALG_NOT_ALLOWED";
		code = "ERR_JOSE_ALG_NOT_ALLOWED";
	};
	exports.JOSEAlgNotAllowed = JOSEAlgNotAllowed;
	var JOSENotSupported = class extends JOSEError {
		static code = "ERR_JOSE_NOT_SUPPORTED";
		code = "ERR_JOSE_NOT_SUPPORTED";
	};
	exports.JOSENotSupported = JOSENotSupported;
	var JWEDecryptionFailed = class extends JOSEError {
		static code = "ERR_JWE_DECRYPTION_FAILED";
		code = "ERR_JWE_DECRYPTION_FAILED";
		constructor(message = "decryption operation failed", options) {
			super(message, options);
		}
	};
	exports.JWEDecryptionFailed = JWEDecryptionFailed;
	var JWEInvalid = class extends JOSEError {
		static code = "ERR_JWE_INVALID";
		code = "ERR_JWE_INVALID";
	};
	exports.JWEInvalid = JWEInvalid;
	var JWSInvalid = class extends JOSEError {
		static code = "ERR_JWS_INVALID";
		code = "ERR_JWS_INVALID";
	};
	exports.JWSInvalid = JWSInvalid;
	var JWTInvalid = class extends JOSEError {
		static code = "ERR_JWT_INVALID";
		code = "ERR_JWT_INVALID";
	};
	exports.JWTInvalid = JWTInvalid;
	var JWKInvalid = class extends JOSEError {
		static code = "ERR_JWK_INVALID";
		code = "ERR_JWK_INVALID";
	};
	exports.JWKInvalid = JWKInvalid;
	var JWKSInvalid = class extends JOSEError {
		static code = "ERR_JWKS_INVALID";
		code = "ERR_JWKS_INVALID";
	};
	exports.JWKSInvalid = JWKSInvalid;
	var JWKSNoMatchingKey = class extends JOSEError {
		static code = "ERR_JWKS_NO_MATCHING_KEY";
		code = "ERR_JWKS_NO_MATCHING_KEY";
		constructor(message = "no applicable key found in the JSON Web Key Set", options) {
			super(message, options);
		}
	};
	exports.JWKSNoMatchingKey = JWKSNoMatchingKey;
	var JWKSMultipleMatchingKeys = class extends JOSEError {
		[Symbol.asyncIterator];
		static code = "ERR_JWKS_MULTIPLE_MATCHING_KEYS";
		code = "ERR_JWKS_MULTIPLE_MATCHING_KEYS";
		constructor(message = "multiple matching keys found in the JSON Web Key Set", options) {
			super(message, options);
		}
	};
	exports.JWKSMultipleMatchingKeys = JWKSMultipleMatchingKeys;
	var JWKSTimeout = class extends JOSEError {
		static code = "ERR_JWKS_TIMEOUT";
		code = "ERR_JWKS_TIMEOUT";
		constructor(message = "request timed out", options) {
			super(message, options);
		}
	};
	exports.JWKSTimeout = JWKSTimeout;
	var JWSSignatureVerificationFailed = class extends JOSEError {
		static code = "ERR_JWS_SIGNATURE_VERIFICATION_FAILED";
		code = "ERR_JWS_SIGNATURE_VERIFICATION_FAILED";
		constructor(message = "signature verification failed", options) {
			super(message, options);
		}
	};
	exports.JWSSignatureVerificationFailed = JWSSignatureVerificationFailed;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/random.js
var require_random = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = void 0;
	var node_crypto_1$16 = __require("node:crypto");
	Object.defineProperty(exports, "default", {
		enumerable: true,
		get: function() {
			return node_crypto_1$16.randomFillSync;
		}
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/iv.js
var require_iv = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.bitLength = bitLength;
	const errors_js_1 = require_errors();
	const random_js_1 = require_random();
	function bitLength(alg) {
		switch (alg) {
			case "A128GCM":
			case "A128GCMKW":
			case "A192GCM":
			case "A192GCMKW":
			case "A256GCM":
			case "A256GCMKW": return 96;
			case "A128CBC-HS256":
			case "A192CBC-HS384":
			case "A256CBC-HS512": return 128;
			default: throw new errors_js_1.JOSENotSupported(`Unsupported JWE Algorithm: ${alg}`);
		}
	}
	exports.default = (alg) => (0, random_js_1.default)(new Uint8Array(bitLength(alg) >> 3));
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/check_iv_length.js
var require_check_iv_length = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const errors_js_1 = require_errors();
	const iv_js_1 = require_iv();
	const checkIvLength = (enc, iv) => {
		if (iv.length << 3 !== (0, iv_js_1.bitLength)(enc)) throw new errors_js_1.JWEInvalid("Invalid Initialization Vector length");
	};
	exports.default = checkIvLength;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/is_key_object.js
var require_is_key_object = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const util$1 = __require("node:util");
	exports.default = (obj) => util$1.types.isKeyObject(obj);
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/check_cek_length.js
var require_check_cek_length = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const errors_js_1 = require_errors();
	const is_key_object_js_1 = require_is_key_object();
	const checkCekLength = (enc, cek) => {
		let expected;
		switch (enc) {
			case "A128CBC-HS256":
			case "A192CBC-HS384":
			case "A256CBC-HS512":
				expected = parseInt(enc.slice(-3), 10);
				break;
			case "A128GCM":
			case "A192GCM":
			case "A256GCM":
				expected = parseInt(enc.slice(1, 4), 10);
				break;
			default: throw new errors_js_1.JOSENotSupported(`Content Encryption Algorithm ${enc} is not supported either by JOSE or your javascript runtime`);
		}
		if (cek instanceof Uint8Array) {
			const actual = cek.byteLength << 3;
			if (actual !== expected) throw new errors_js_1.JWEInvalid(`Invalid Content Encryption Key length. Expected ${expected} bits, got ${actual} bits`);
			return;
		}
		if ((0, is_key_object_js_1.default)(cek) && cek.type === "secret") {
			const actual = cek.symmetricKeySize << 3;
			if (actual !== expected) throw new errors_js_1.JWEInvalid(`Invalid Content Encryption Key length. Expected ${expected} bits, got ${actual} bits`);
			return;
		}
		throw new TypeError("Invalid Content Encryption Key type");
	};
	exports.default = checkCekLength;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/timing_safe_equal.js
var require_timing_safe_equal = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = __require("node:crypto").timingSafeEqual;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/cbc_tag.js
var require_cbc_tag = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = cbcTag;
	const node_crypto_1$15 = __require("node:crypto");
	const buffer_utils_js_1 = require_buffer_utils();
	function cbcTag(aad, iv, ciphertext, macSize, macKey, keySize) {
		const macData = (0, buffer_utils_js_1.concat)(aad, iv, ciphertext, (0, buffer_utils_js_1.uint64be)(aad.length << 3));
		const hmac = (0, node_crypto_1$15.createHmac)(`sha${macSize}`, macKey);
		hmac.update(macData);
		return hmac.digest().slice(0, keySize >> 3);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/webcrypto.js
var require_webcrypto = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.isCryptoKey = void 0;
	const crypto$3 = __require("node:crypto");
	const util = __require("node:util");
	exports.default = crypto$3.webcrypto;
	const isCryptoKey = (key) => util.types.isCryptoKey(key);
	exports.isCryptoKey = isCryptoKey;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/crypto_key.js
var require_crypto_key = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.checkSigCryptoKey = checkSigCryptoKey;
	exports.checkEncCryptoKey = checkEncCryptoKey;
	function unusable(name, prop = "algorithm.name") {
		return /* @__PURE__ */ new TypeError(`CryptoKey does not support this operation, its ${prop} must be ${name}`);
	}
	function isAlgorithm(algorithm, name) {
		return algorithm.name === name;
	}
	function getHashLength(hash) {
		return parseInt(hash.name.slice(4), 10);
	}
	function getNamedCurve(alg) {
		switch (alg) {
			case "ES256": return "P-256";
			case "ES384": return "P-384";
			case "ES512": return "P-521";
			default: throw new Error("unreachable");
		}
	}
	function checkUsage(key, usages) {
		if (usages.length && !usages.some((expected) => key.usages.includes(expected))) {
			let msg = "CryptoKey does not support this operation, its usages must include ";
			if (usages.length > 2) {
				const last = usages.pop();
				msg += `one of ${usages.join(", ")}, or ${last}.`;
			} else if (usages.length === 2) msg += `one of ${usages[0]} or ${usages[1]}.`;
			else msg += `${usages[0]}.`;
			throw new TypeError(msg);
		}
	}
	function checkSigCryptoKey(key, alg, ...usages) {
		switch (alg) {
			case "HS256":
			case "HS384":
			case "HS512": {
				if (!isAlgorithm(key.algorithm, "HMAC")) throw unusable("HMAC");
				const expected = parseInt(alg.slice(2), 10);
				if (getHashLength(key.algorithm.hash) !== expected) throw unusable(`SHA-${expected}`, "algorithm.hash");
				break;
			}
			case "RS256":
			case "RS384":
			case "RS512": {
				if (!isAlgorithm(key.algorithm, "RSASSA-PKCS1-v1_5")) throw unusable("RSASSA-PKCS1-v1_5");
				const expected = parseInt(alg.slice(2), 10);
				if (getHashLength(key.algorithm.hash) !== expected) throw unusable(`SHA-${expected}`, "algorithm.hash");
				break;
			}
			case "PS256":
			case "PS384":
			case "PS512": {
				if (!isAlgorithm(key.algorithm, "RSA-PSS")) throw unusable("RSA-PSS");
				const expected = parseInt(alg.slice(2), 10);
				if (getHashLength(key.algorithm.hash) !== expected) throw unusable(`SHA-${expected}`, "algorithm.hash");
				break;
			}
			case "EdDSA":
				if (key.algorithm.name !== "Ed25519" && key.algorithm.name !== "Ed448") throw unusable("Ed25519 or Ed448");
				break;
			case "Ed25519":
				if (!isAlgorithm(key.algorithm, "Ed25519")) throw unusable("Ed25519");
				break;
			case "ES256":
			case "ES384":
			case "ES512": {
				if (!isAlgorithm(key.algorithm, "ECDSA")) throw unusable("ECDSA");
				const expected = getNamedCurve(alg);
				if (key.algorithm.namedCurve !== expected) throw unusable(expected, "algorithm.namedCurve");
				break;
			}
			default: throw new TypeError("CryptoKey does not support this operation");
		}
		checkUsage(key, usages);
	}
	function checkEncCryptoKey(key, alg, ...usages) {
		switch (alg) {
			case "A128GCM":
			case "A192GCM":
			case "A256GCM": {
				if (!isAlgorithm(key.algorithm, "AES-GCM")) throw unusable("AES-GCM");
				const expected = parseInt(alg.slice(1, 4), 10);
				if (key.algorithm.length !== expected) throw unusable(expected, "algorithm.length");
				break;
			}
			case "A128KW":
			case "A192KW":
			case "A256KW": {
				if (!isAlgorithm(key.algorithm, "AES-KW")) throw unusable("AES-KW");
				const expected = parseInt(alg.slice(1, 4), 10);
				if (key.algorithm.length !== expected) throw unusable(expected, "algorithm.length");
				break;
			}
			case "ECDH":
				switch (key.algorithm.name) {
					case "ECDH":
					case "X25519":
					case "X448": break;
					default: throw unusable("ECDH, X25519, or X448");
				}
				break;
			case "PBES2-HS256+A128KW":
			case "PBES2-HS384+A192KW":
			case "PBES2-HS512+A256KW":
				if (!isAlgorithm(key.algorithm, "PBKDF2")) throw unusable("PBKDF2");
				break;
			case "RSA-OAEP":
			case "RSA-OAEP-256":
			case "RSA-OAEP-384":
			case "RSA-OAEP-512": {
				if (!isAlgorithm(key.algorithm, "RSA-OAEP")) throw unusable("RSA-OAEP");
				const expected = parseInt(alg.slice(9), 10) || 1;
				if (getHashLength(key.algorithm.hash) !== expected) throw unusable(`SHA-${expected}`, "algorithm.hash");
				break;
			}
			default: throw new TypeError("CryptoKey does not support this operation");
		}
		checkUsage(key, usages);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/invalid_key_input.js
var require_invalid_key_input = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.withAlg = withAlg;
	function message(msg, actual, ...types) {
		types = types.filter(Boolean);
		if (types.length > 2) {
			const last = types.pop();
			msg += `one of type ${types.join(", ")}, or ${last}.`;
		} else if (types.length === 2) msg += `one of type ${types[0]} or ${types[1]}.`;
		else msg += `of type ${types[0]}.`;
		if (actual == null) msg += ` Received ${actual}`;
		else if (typeof actual === "function" && actual.name) msg += ` Received function ${actual.name}`;
		else if (typeof actual === "object" && actual != null) {
			if (actual.constructor?.name) msg += ` Received an instance of ${actual.constructor.name}`;
		}
		return msg;
	}
	exports.default = (actual, ...types) => {
		return message("Key must be ", actual, ...types);
	};
	function withAlg(alg, actual, ...types) {
		return message(`Key for the ${alg} algorithm must be `, actual, ...types);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/ciphers.js
var require_ciphers = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$14 = __require("node:crypto");
	let ciphers;
	exports.default = (algorithm) => {
		ciphers ||= new Set((0, node_crypto_1$14.getCiphers)());
		return ciphers.has(algorithm);
	};
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/is_key_like.js
var require_is_key_like = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.types = void 0;
	const webcrypto_js_1 = require_webcrypto();
	const is_key_object_js_1 = require_is_key_object();
	exports.default = (key) => (0, is_key_object_js_1.default)(key) || (0, webcrypto_js_1.isCryptoKey)(key);
	const types = ["KeyObject"];
	exports.types = types;
	if (globalThis.CryptoKey || webcrypto_js_1.default?.CryptoKey) types.push("CryptoKey");
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/decrypt.js
var require_decrypt$4 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$13 = __require("node:crypto");
	const check_iv_length_js_1 = require_check_iv_length();
	const check_cek_length_js_1 = require_check_cek_length();
	const buffer_utils_js_1 = require_buffer_utils();
	const errors_js_1 = require_errors();
	const timing_safe_equal_js_1 = require_timing_safe_equal();
	const cbc_tag_js_1 = require_cbc_tag();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const ciphers_js_1 = require_ciphers();
	const is_key_like_js_1 = require_is_key_like();
	function cbcDecrypt(enc, cek, ciphertext, iv, tag, aad) {
		const keySize = parseInt(enc.slice(1, 4), 10);
		if ((0, is_key_object_js_1.default)(cek)) cek = cek.export();
		const encKey = cek.subarray(keySize >> 3);
		const macKey = cek.subarray(0, keySize >> 3);
		const macSize = parseInt(enc.slice(-3), 10);
		const algorithm = `aes-${keySize}-cbc`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${enc} is not supported by your javascript runtime`);
		const expectedTag = (0, cbc_tag_js_1.default)(aad, iv, ciphertext, macSize, macKey, keySize);
		let macCheckPassed;
		try {
			macCheckPassed = (0, timing_safe_equal_js_1.default)(tag, expectedTag);
		} catch {}
		if (!macCheckPassed) throw new errors_js_1.JWEDecryptionFailed();
		let plaintext;
		try {
			const decipher = (0, node_crypto_1$13.createDecipheriv)(algorithm, encKey, iv);
			plaintext = (0, buffer_utils_js_1.concat)(decipher.update(ciphertext), decipher.final());
		} catch {}
		if (!plaintext) throw new errors_js_1.JWEDecryptionFailed();
		return plaintext;
	}
	function gcmDecrypt(enc, cek, ciphertext, iv, tag, aad) {
		const algorithm = `aes-${parseInt(enc.slice(1, 4), 10)}-gcm`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${enc} is not supported by your javascript runtime`);
		try {
			const decipher = (0, node_crypto_1$13.createDecipheriv)(algorithm, cek, iv, { authTagLength: 16 });
			decipher.setAuthTag(tag);
			if (aad.byteLength) decipher.setAAD(aad, { plaintextLength: ciphertext.length });
			const plaintext = decipher.update(ciphertext);
			decipher.final();
			return plaintext;
		} catch {
			throw new errors_js_1.JWEDecryptionFailed();
		}
	}
	const decrypt = (enc, cek, ciphertext, iv, tag, aad) => {
		let key;
		if ((0, webcrypto_js_1.isCryptoKey)(cek)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(cek, enc, "decrypt");
			key = node_crypto_1$13.KeyObject.from(cek);
		} else if (cek instanceof Uint8Array || (0, is_key_object_js_1.default)(cek)) key = cek;
		else throw new TypeError((0, invalid_key_input_js_1.default)(cek, ...is_key_like_js_1.types, "Uint8Array"));
		if (!iv) throw new errors_js_1.JWEInvalid("JWE Initialization Vector missing");
		if (!tag) throw new errors_js_1.JWEInvalid("JWE Authentication Tag missing");
		(0, check_cek_length_js_1.default)(enc, key);
		(0, check_iv_length_js_1.default)(enc, iv);
		switch (enc) {
			case "A128CBC-HS256":
			case "A192CBC-HS384":
			case "A256CBC-HS512": return cbcDecrypt(enc, key, ciphertext, iv, tag, aad);
			case "A128GCM":
			case "A192GCM":
			case "A256GCM": return gcmDecrypt(enc, key, ciphertext, iv, tag, aad);
			default: throw new errors_js_1.JOSENotSupported("Unsupported JWE Content Encryption Algorithm");
		}
	};
	exports.default = decrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/is_disjoint.js
var require_is_disjoint = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const isDisjoint = (...headers) => {
		const sources = headers.filter(Boolean);
		if (sources.length === 0 || sources.length === 1) return true;
		let acc;
		for (const header of sources) {
			const parameters = Object.keys(header);
			if (!acc || acc.size === 0) {
				acc = new Set(parameters);
				continue;
			}
			for (const parameter of parameters) {
				if (acc.has(parameter)) return false;
				acc.add(parameter);
			}
		}
		return true;
	};
	exports.default = isDisjoint;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/is_object.js
var require_is_object = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = isObject;
	function isObjectLike(value) {
		return typeof value === "object" && value !== null;
	}
	function isObject(input) {
		if (!isObjectLike(input) || Object.prototype.toString.call(input) !== "[object Object]") return false;
		if (Object.getPrototypeOf(input) === null) return true;
		let proto = input;
		while (Object.getPrototypeOf(proto) !== null) proto = Object.getPrototypeOf(proto);
		return Object.getPrototypeOf(input) === proto;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/aeskw.js
var require_aeskw = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.unwrap = exports.wrap = void 0;
	const node_buffer_1$1 = __require("node:buffer");
	const node_crypto_1$12 = __require("node:crypto");
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const ciphers_js_1 = require_ciphers();
	const is_key_like_js_1 = require_is_key_like();
	function checkKeySize(key, alg) {
		if (key.symmetricKeySize << 3 !== parseInt(alg.slice(1, 4), 10)) throw new TypeError(`Invalid key size for alg: ${alg}`);
	}
	function ensureKeyObject(key, alg, usage) {
		if ((0, is_key_object_js_1.default)(key)) return key;
		if (key instanceof Uint8Array) return (0, node_crypto_1$12.createSecretKey)(key);
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(key, alg, usage);
			return node_crypto_1$12.KeyObject.from(key);
		}
		throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types, "Uint8Array"));
	}
	const wrap = (alg, key, cek) => {
		const algorithm = `aes${parseInt(alg.slice(1, 4), 10)}-wrap`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
		const keyObject = ensureKeyObject(key, alg, "wrapKey");
		checkKeySize(keyObject, alg);
		const cipher = (0, node_crypto_1$12.createCipheriv)(algorithm, keyObject, node_buffer_1$1.Buffer.alloc(8, 166));
		return (0, buffer_utils_js_1.concat)(cipher.update(cek), cipher.final());
	};
	exports.wrap = wrap;
	const unwrap = (alg, key, encryptedKey) => {
		const algorithm = `aes${parseInt(alg.slice(1, 4), 10)}-wrap`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
		const keyObject = ensureKeyObject(key, alg, "unwrapKey");
		checkKeySize(keyObject, alg);
		const cipher = (0, node_crypto_1$12.createDecipheriv)(algorithm, keyObject, node_buffer_1$1.Buffer.alloc(8, 166));
		return (0, buffer_utils_js_1.concat)(cipher.update(encryptedKey), cipher.final());
	};
	exports.unwrap = unwrap;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/is_jwk.js
var require_is_jwk = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.isJWK = isJWK;
	exports.isPrivateJWK = isPrivateJWK;
	exports.isPublicJWK = isPublicJWK;
	exports.isSecretJWK = isSecretJWK;
	const is_object_js_1 = require_is_object();
	function isJWK(key) {
		return (0, is_object_js_1.default)(key) && typeof key.kty === "string";
	}
	function isPrivateJWK(key) {
		return key.kty !== "oct" && typeof key.d === "string";
	}
	function isPublicJWK(key) {
		return key.kty !== "oct" && typeof key.d === "undefined";
	}
	function isSecretJWK(key) {
		return isJWK(key) && key.kty === "oct" && typeof key.k === "string";
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/get_named_curve.js
var require_get_named_curve = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.weakMap = void 0;
	const node_crypto_1$11 = __require("node:crypto");
	const errors_js_1 = require_errors();
	const webcrypto_js_1 = require_webcrypto();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const is_jwk_js_1 = require_is_jwk();
	exports.weakMap = /* @__PURE__ */ new WeakMap();
	const namedCurveToJOSE = (namedCurve) => {
		switch (namedCurve) {
			case "prime256v1": return "P-256";
			case "secp384r1": return "P-384";
			case "secp521r1": return "P-521";
			case "secp256k1": return "secp256k1";
			default: throw new errors_js_1.JOSENotSupported("Unsupported key curve for this operation");
		}
	};
	const getNamedCurve = (kee, raw) => {
		let key;
		if ((0, webcrypto_js_1.isCryptoKey)(kee)) key = node_crypto_1$11.KeyObject.from(kee);
		else if ((0, is_key_object_js_1.default)(kee)) key = kee;
		else if ((0, is_jwk_js_1.isJWK)(kee)) return kee.crv;
		else throw new TypeError((0, invalid_key_input_js_1.default)(kee, ...is_key_like_js_1.types));
		if (key.type === "secret") throw new TypeError("only \"private\" or \"public\" type keys can be used for this operation");
		switch (key.asymmetricKeyType) {
			case "ed25519":
			case "ed448": return `Ed${key.asymmetricKeyType.slice(2)}`;
			case "x25519":
			case "x448": return `X${key.asymmetricKeyType.slice(1)}`;
			case "ec": {
				const namedCurve = key.asymmetricKeyDetails.namedCurve;
				if (raw) return namedCurve;
				return namedCurveToJOSE(namedCurve);
			}
			default: throw new TypeError("Invalid asymmetric key type for this operation");
		}
	};
	exports.default = getNamedCurve;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/ecdhes.js
var require_ecdhes = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.ecdhAllowed = void 0;
	exports.deriveKey = deriveKey;
	exports.generateEpk = generateEpk;
	const node_crypto_1$10 = __require("node:crypto");
	const node_util_1$5 = __require("node:util");
	const get_named_curve_js_1 = require_get_named_curve();
	const buffer_utils_js_1 = require_buffer_utils();
	const errors_js_1 = require_errors();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const generateKeyPair = (0, node_util_1$5.promisify)(node_crypto_1$10.generateKeyPair);
	async function deriveKey(publicKee, privateKee, algorithm, keyLength, apu = /* @__PURE__ */ new Uint8Array(0), apv = /* @__PURE__ */ new Uint8Array(0)) {
		let publicKey;
		if ((0, webcrypto_js_1.isCryptoKey)(publicKee)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(publicKee, "ECDH");
			publicKey = node_crypto_1$10.KeyObject.from(publicKee);
		} else if ((0, is_key_object_js_1.default)(publicKee)) publicKey = publicKee;
		else throw new TypeError((0, invalid_key_input_js_1.default)(publicKee, ...is_key_like_js_1.types));
		let privateKey;
		if ((0, webcrypto_js_1.isCryptoKey)(privateKee)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(privateKee, "ECDH", "deriveBits");
			privateKey = node_crypto_1$10.KeyObject.from(privateKee);
		} else if ((0, is_key_object_js_1.default)(privateKee)) privateKey = privateKee;
		else throw new TypeError((0, invalid_key_input_js_1.default)(privateKee, ...is_key_like_js_1.types));
		const value = (0, buffer_utils_js_1.concat)((0, buffer_utils_js_1.lengthAndInput)(buffer_utils_js_1.encoder.encode(algorithm)), (0, buffer_utils_js_1.lengthAndInput)(apu), (0, buffer_utils_js_1.lengthAndInput)(apv), (0, buffer_utils_js_1.uint32be)(keyLength));
		const sharedSecret = (0, node_crypto_1$10.diffieHellman)({
			privateKey,
			publicKey
		});
		return (0, buffer_utils_js_1.concatKdf)(sharedSecret, keyLength, value);
	}
	async function generateEpk(kee) {
		let key;
		if ((0, webcrypto_js_1.isCryptoKey)(kee)) key = node_crypto_1$10.KeyObject.from(kee);
		else if ((0, is_key_object_js_1.default)(kee)) key = kee;
		else throw new TypeError((0, invalid_key_input_js_1.default)(kee, ...is_key_like_js_1.types));
		switch (key.asymmetricKeyType) {
			case "x25519": return generateKeyPair("x25519");
			case "x448": return generateKeyPair("x448");
			case "ec": {
				const namedCurve = (0, get_named_curve_js_1.default)(key);
				return generateKeyPair("ec", { namedCurve });
			}
			default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported EPK");
		}
	}
	const ecdhAllowed = (key) => [
		"P-256",
		"P-384",
		"P-521",
		"X25519",
		"X448"
	].includes((0, get_named_curve_js_1.default)(key));
	exports.ecdhAllowed = ecdhAllowed;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/check_p2s.js
var require_check_p2s = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = checkP2s;
	const errors_js_1 = require_errors();
	function checkP2s(p2s) {
		if (!(p2s instanceof Uint8Array) || p2s.length < 8) throw new errors_js_1.JWEInvalid("PBES2 Salt Input must be 8 or more octets");
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/pbes2kw.js
var require_pbes2kw = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decrypt = exports.encrypt = void 0;
	const node_util_1$4 = __require("node:util");
	const node_crypto_1$9 = __require("node:crypto");
	const random_js_1 = require_random();
	const buffer_utils_js_1 = require_buffer_utils();
	const base64url_js_1 = require_base64url$1();
	const aeskw_js_1 = require_aeskw();
	const check_p2s_js_1 = require_check_p2s();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const pbkdf2 = (0, node_util_1$4.promisify)(node_crypto_1$9.pbkdf2);
	function getPassword(key, alg) {
		if ((0, is_key_object_js_1.default)(key)) return key.export();
		if (key instanceof Uint8Array) return key;
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(key, alg, "deriveBits", "deriveKey");
			return node_crypto_1$9.KeyObject.from(key).export();
		}
		throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types, "Uint8Array"));
	}
	const encrypt = async (alg, key, cek, p2c = 2048, p2s = (0, random_js_1.default)(/* @__PURE__ */ new Uint8Array(16))) => {
		(0, check_p2s_js_1.default)(p2s);
		const salt = (0, buffer_utils_js_1.p2s)(alg, p2s);
		const keylen = parseInt(alg.slice(13, 16), 10) >> 3;
		const password = getPassword(key, alg);
		const derivedKey = await pbkdf2(password, salt, p2c, keylen, `sha${alg.slice(8, 11)}`);
		return {
			encryptedKey: await (0, aeskw_js_1.wrap)(alg.slice(-6), derivedKey, cek),
			p2c,
			p2s: (0, base64url_js_1.encode)(p2s)
		};
	};
	exports.encrypt = encrypt;
	const decrypt = async (alg, key, encryptedKey, p2c, p2s) => {
		(0, check_p2s_js_1.default)(p2s);
		const salt = (0, buffer_utils_js_1.p2s)(alg, p2s);
		const keylen = parseInt(alg.slice(13, 16), 10) >> 3;
		const password = getPassword(key, alg);
		const derivedKey = await pbkdf2(password, salt, p2c, keylen, `sha${alg.slice(8, 11)}`);
		return (0, aeskw_js_1.unwrap)(alg.slice(-6), derivedKey, encryptedKey);
	};
	exports.decrypt = decrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/check_key_length.js
var require_check_key_length = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$8 = __require("node:crypto");
	exports.default = (key, alg) => {
		let modulusLength;
		try {
			if (key instanceof node_crypto_1$8.KeyObject) modulusLength = key.asymmetricKeyDetails?.modulusLength;
			else modulusLength = Buffer.from(key.n, "base64url").byteLength << 3;
		} catch {}
		if (typeof modulusLength !== "number" || modulusLength < 2048) throw new TypeError(`${alg} requires key modulusLength to be 2048 bits or larger`);
	};
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/rsaes.js
var require_rsaes = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decrypt = exports.encrypt = void 0;
	const node_crypto_1$7 = __require("node:crypto");
	const node_util_1$3 = __require("node:util");
	const check_key_length_js_1 = require_check_key_length();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const checkKey = (key, alg) => {
		if (key.asymmetricKeyType !== "rsa") throw new TypeError("Invalid key for this operation, its asymmetricKeyType must be rsa");
		(0, check_key_length_js_1.default)(key, alg);
	};
	const RSA1_5 = (0, node_util_1$3.deprecate)(() => node_crypto_1$7.constants.RSA_PKCS1_PADDING, "The RSA1_5 \"alg\" (JWE Algorithm) is deprecated and will be removed in the next major revision.");
	const resolvePadding = (alg) => {
		switch (alg) {
			case "RSA-OAEP":
			case "RSA-OAEP-256":
			case "RSA-OAEP-384":
			case "RSA-OAEP-512": return node_crypto_1$7.constants.RSA_PKCS1_OAEP_PADDING;
			case "RSA1_5": return RSA1_5();
			default: return;
		}
	};
	const resolveOaepHash = (alg) => {
		switch (alg) {
			case "RSA-OAEP": return "sha1";
			case "RSA-OAEP-256": return "sha256";
			case "RSA-OAEP-384": return "sha384";
			case "RSA-OAEP-512": return "sha512";
			default: return;
		}
	};
	function ensureKeyObject(key, alg, ...usages) {
		if ((0, is_key_object_js_1.default)(key)) return key;
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(key, alg, ...usages);
			return node_crypto_1$7.KeyObject.from(key);
		}
		throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types));
	}
	const encrypt = (alg, key, cek) => {
		const padding = resolvePadding(alg);
		const oaepHash = resolveOaepHash(alg);
		const keyObject = ensureKeyObject(key, alg, "wrapKey", "encrypt");
		checkKey(keyObject, alg);
		return (0, node_crypto_1$7.publicEncrypt)({
			key: keyObject,
			oaepHash,
			padding
		}, cek);
	};
	exports.encrypt = encrypt;
	const decrypt = (alg, key, encryptedKey) => {
		const padding = resolvePadding(alg);
		const oaepHash = resolveOaepHash(alg);
		const keyObject = ensureKeyObject(key, alg, "unwrapKey", "decrypt");
		checkKey(keyObject, alg);
		return (0, node_crypto_1$7.privateDecrypt)({
			key: keyObject,
			oaepHash,
			padding
		}, encryptedKey);
	};
	exports.decrypt = decrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/normalize_key.js
var require_normalize_key = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = {};
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/cek.js
var require_cek = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.bitLength = bitLength;
	const errors_js_1 = require_errors();
	const random_js_1 = require_random();
	function bitLength(alg) {
		switch (alg) {
			case "A128GCM": return 128;
			case "A192GCM": return 192;
			case "A256GCM":
			case "A128CBC-HS256": return 256;
			case "A192CBC-HS384": return 384;
			case "A256CBC-HS512": return 512;
			default: throw new errors_js_1.JOSENotSupported(`Unsupported JWE Algorithm: ${alg}`);
		}
	}
	exports.default = (alg) => (0, random_js_1.default)(new Uint8Array(bitLength(alg) >> 3));
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/asn1.js
var require_asn1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.fromX509 = exports.fromSPKI = exports.fromPKCS8 = exports.toPKCS8 = exports.toSPKI = void 0;
	const node_crypto_1$6 = __require("node:crypto");
	const node_buffer_1 = __require("node:buffer");
	const webcrypto_js_1 = require_webcrypto();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const genericExport = (keyType, keyFormat, key) => {
		let keyObject;
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			if (!key.extractable) throw new TypeError("CryptoKey is not extractable");
			keyObject = node_crypto_1$6.KeyObject.from(key);
		} else if ((0, is_key_object_js_1.default)(key)) keyObject = key;
		else throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types));
		if (keyObject.type !== keyType) throw new TypeError(`key is not a ${keyType} key`);
		return keyObject.export({
			format: "pem",
			type: keyFormat
		});
	};
	const toSPKI = (key) => {
		return genericExport("public", "spki", key);
	};
	exports.toSPKI = toSPKI;
	const toPKCS8 = (key) => {
		return genericExport("private", "pkcs8", key);
	};
	exports.toPKCS8 = toPKCS8;
	const fromPKCS8 = (pem) => (0, node_crypto_1$6.createPrivateKey)({
		key: node_buffer_1.Buffer.from(pem.replace(/(?:-----(?:BEGIN|END) PRIVATE KEY-----|\s)/g, ""), "base64"),
		type: "pkcs8",
		format: "der"
	});
	exports.fromPKCS8 = fromPKCS8;
	const fromSPKI = (pem) => (0, node_crypto_1$6.createPublicKey)({
		key: node_buffer_1.Buffer.from(pem.replace(/(?:-----(?:BEGIN|END) PUBLIC KEY-----|\s)/g, ""), "base64"),
		type: "spki",
		format: "der"
	});
	exports.fromSPKI = fromSPKI;
	const fromX509 = (pem) => (0, node_crypto_1$6.createPublicKey)({
		key: pem,
		type: "spki",
		format: "pem"
	});
	exports.fromX509 = fromX509;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/jwk_to_key.js
var require_jwk_to_key = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$5 = __require("node:crypto");
	const parse = (key) => {
		if (key.d) return (0, node_crypto_1$5.createPrivateKey)({
			format: "jwk",
			key
		});
		return (0, node_crypto_1$5.createPublicKey)({
			format: "jwk",
			key
		});
	};
	exports.default = parse;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/key/import.js
var require_import = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.importSPKI = importSPKI;
	exports.importX509 = importX509;
	exports.importPKCS8 = importPKCS8;
	exports.importJWK = importJWK;
	const base64url_js_1 = require_base64url$1();
	const asn1_js_1 = require_asn1();
	const jwk_to_key_js_1 = require_jwk_to_key();
	const errors_js_1 = require_errors();
	const is_object_js_1 = require_is_object();
	async function importSPKI(spki, alg, options) {
		if (typeof spki !== "string" || spki.indexOf("-----BEGIN PUBLIC KEY-----") !== 0) throw new TypeError("\"spki\" must be SPKI formatted string");
		return (0, asn1_js_1.fromSPKI)(spki, alg, options);
	}
	async function importX509(x509, alg, options) {
		if (typeof x509 !== "string" || x509.indexOf("-----BEGIN CERTIFICATE-----") !== 0) throw new TypeError("\"x509\" must be X.509 formatted string");
		return (0, asn1_js_1.fromX509)(x509, alg, options);
	}
	async function importPKCS8(pkcs8, alg, options) {
		if (typeof pkcs8 !== "string" || pkcs8.indexOf("-----BEGIN PRIVATE KEY-----") !== 0) throw new TypeError("\"pkcs8\" must be PKCS#8 formatted string");
		return (0, asn1_js_1.fromPKCS8)(pkcs8, alg, options);
	}
	async function importJWK(jwk, alg) {
		if (!(0, is_object_js_1.default)(jwk)) throw new TypeError("JWK must be an object");
		alg ||= jwk.alg;
		switch (jwk.kty) {
			case "oct":
				if (typeof jwk.k !== "string" || !jwk.k) throw new TypeError("missing \"k\" (Key Value) Parameter value");
				return (0, base64url_js_1.decode)(jwk.k);
			case "RSA": if ("oth" in jwk && jwk.oth !== void 0) throw new errors_js_1.JOSENotSupported("RSA JWK \"oth\" (Other Primes Info) Parameter value is not supported");
			case "EC":
			case "OKP": return (0, jwk_to_key_js_1.default)({
				...jwk,
				alg
			});
			default: throw new errors_js_1.JOSENotSupported("Unsupported \"kty\" (Key Type) Parameter value");
		}
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/check_key_type.js
var require_check_key_type = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.checkKeyTypeWithJwk = void 0;
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const jwk = require_is_jwk();
	const tag = (key) => key?.[Symbol.toStringTag];
	const jwkMatchesOp = (alg, key, usage) => {
		if (key.use !== void 0 && key.use !== "sig") throw new TypeError("Invalid key for this operation, when present its use must be sig");
		if (key.key_ops !== void 0 && key.key_ops.includes?.(usage) !== true) throw new TypeError(`Invalid key for this operation, when present its key_ops must include ${usage}`);
		if (key.alg !== void 0 && key.alg !== alg) throw new TypeError(`Invalid key for this operation, when present its alg must be ${alg}`);
		return true;
	};
	const symmetricTypeCheck = (alg, key, usage, allowJwk) => {
		if (key instanceof Uint8Array) return;
		if (allowJwk && jwk.isJWK(key)) {
			if (jwk.isSecretJWK(key) && jwkMatchesOp(alg, key, usage)) return;
			throw new TypeError(`JSON Web Key for symmetric algorithms must have JWK "kty" (Key Type) equal to "oct" and the JWK "k" (Key Value) present`);
		}
		if (!(0, is_key_like_js_1.default)(key)) throw new TypeError((0, invalid_key_input_js_1.withAlg)(alg, key, ...is_key_like_js_1.types, "Uint8Array", allowJwk ? "JSON Web Key" : null));
		if (key.type !== "secret") throw new TypeError(`${tag(key)} instances for symmetric algorithms must be of type "secret"`);
	};
	const asymmetricTypeCheck = (alg, key, usage, allowJwk) => {
		if (allowJwk && jwk.isJWK(key)) switch (usage) {
			case "sign":
				if (jwk.isPrivateJWK(key) && jwkMatchesOp(alg, key, usage)) return;
				throw new TypeError(`JSON Web Key for this operation be a private JWK`);
			case "verify":
				if (jwk.isPublicJWK(key) && jwkMatchesOp(alg, key, usage)) return;
				throw new TypeError(`JSON Web Key for this operation be a public JWK`);
		}
		if (!(0, is_key_like_js_1.default)(key)) throw new TypeError((0, invalid_key_input_js_1.withAlg)(alg, key, ...is_key_like_js_1.types, allowJwk ? "JSON Web Key" : null));
		if (key.type === "secret") throw new TypeError(`${tag(key)} instances for asymmetric algorithms must not be of type "secret"`);
		if (usage === "sign" && key.type === "public") throw new TypeError(`${tag(key)} instances for asymmetric algorithm signing must be of type "private"`);
		if (usage === "decrypt" && key.type === "public") throw new TypeError(`${tag(key)} instances for asymmetric algorithm decryption must be of type "private"`);
		if (key.algorithm && usage === "verify" && key.type === "private") throw new TypeError(`${tag(key)} instances for asymmetric algorithm verifying must be of type "public"`);
		if (key.algorithm && usage === "encrypt" && key.type === "private") throw new TypeError(`${tag(key)} instances for asymmetric algorithm encryption must be of type "public"`);
	};
	function checkKeyType(allowJwk, alg, key, usage) {
		if (alg.startsWith("HS") || alg === "dir" || alg.startsWith("PBES2") || /^A\d{3}(?:GCM)?KW$/.test(alg)) symmetricTypeCheck(alg, key, usage, allowJwk);
		else asymmetricTypeCheck(alg, key, usage, allowJwk);
	}
	exports.default = checkKeyType.bind(void 0, false);
	exports.checkKeyTypeWithJwk = checkKeyType.bind(void 0, true);
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/encrypt.js
var require_encrypt$4 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$4 = __require("node:crypto");
	const check_iv_length_js_1 = require_check_iv_length();
	const check_cek_length_js_1 = require_check_cek_length();
	const buffer_utils_js_1 = require_buffer_utils();
	const cbc_tag_js_1 = require_cbc_tag();
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const iv_js_1 = require_iv();
	const errors_js_1 = require_errors();
	const ciphers_js_1 = require_ciphers();
	const is_key_like_js_1 = require_is_key_like();
	function cbcEncrypt(enc, plaintext, cek, iv, aad) {
		const keySize = parseInt(enc.slice(1, 4), 10);
		if ((0, is_key_object_js_1.default)(cek)) cek = cek.export();
		const encKey = cek.subarray(keySize >> 3);
		const macKey = cek.subarray(0, keySize >> 3);
		const algorithm = `aes-${keySize}-cbc`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${enc} is not supported by your javascript runtime`);
		const cipher = (0, node_crypto_1$4.createCipheriv)(algorithm, encKey, iv);
		const ciphertext = (0, buffer_utils_js_1.concat)(cipher.update(plaintext), cipher.final());
		const macSize = parseInt(enc.slice(-3), 10);
		return {
			ciphertext,
			tag: (0, cbc_tag_js_1.default)(aad, iv, ciphertext, macSize, macKey, keySize),
			iv
		};
	}
	function gcmEncrypt(enc, plaintext, cek, iv, aad) {
		const algorithm = `aes-${parseInt(enc.slice(1, 4), 10)}-gcm`;
		if (!(0, ciphers_js_1.default)(algorithm)) throw new errors_js_1.JOSENotSupported(`alg ${enc} is not supported by your javascript runtime`);
		const cipher = (0, node_crypto_1$4.createCipheriv)(algorithm, cek, iv, { authTagLength: 16 });
		if (aad.byteLength) cipher.setAAD(aad, { plaintextLength: plaintext.length });
		const ciphertext = cipher.update(plaintext);
		cipher.final();
		return {
			ciphertext,
			tag: cipher.getAuthTag(),
			iv
		};
	}
	const encrypt = (enc, plaintext, cek, iv, aad) => {
		let key;
		if ((0, webcrypto_js_1.isCryptoKey)(cek)) {
			(0, crypto_key_js_1.checkEncCryptoKey)(cek, enc, "encrypt");
			key = node_crypto_1$4.KeyObject.from(cek);
		} else if (cek instanceof Uint8Array || (0, is_key_object_js_1.default)(cek)) key = cek;
		else throw new TypeError((0, invalid_key_input_js_1.default)(cek, ...is_key_like_js_1.types, "Uint8Array"));
		(0, check_cek_length_js_1.default)(enc, key);
		if (iv) (0, check_iv_length_js_1.default)(enc, iv);
		else iv = (0, iv_js_1.default)(enc);
		switch (enc) {
			case "A128CBC-HS256":
			case "A192CBC-HS384":
			case "A256CBC-HS512": return cbcEncrypt(enc, plaintext, key, iv, aad);
			case "A128GCM":
			case "A192GCM":
			case "A256GCM": return gcmEncrypt(enc, plaintext, key, iv, aad);
			default: throw new errors_js_1.JOSENotSupported("Unsupported JWE Content Encryption Algorithm");
		}
	};
	exports.default = encrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/aesgcmkw.js
var require_aesgcmkw = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.wrap = wrap;
	exports.unwrap = unwrap;
	const encrypt_js_1 = require_encrypt$4();
	const decrypt_js_1 = require_decrypt$4();
	const base64url_js_1 = require_base64url$1();
	async function wrap(alg, key, cek, iv) {
		const jweAlgorithm = alg.slice(0, 7);
		const wrapped = await (0, encrypt_js_1.default)(jweAlgorithm, cek, key, iv, /* @__PURE__ */ new Uint8Array(0));
		return {
			encryptedKey: wrapped.ciphertext,
			iv: (0, base64url_js_1.encode)(wrapped.iv),
			tag: (0, base64url_js_1.encode)(wrapped.tag)
		};
	}
	async function unwrap(alg, key, encryptedKey, iv, tag) {
		const jweAlgorithm = alg.slice(0, 7);
		return (0, decrypt_js_1.default)(jweAlgorithm, key, encryptedKey, iv, tag, /* @__PURE__ */ new Uint8Array(0));
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/decrypt_key_management.js
var require_decrypt_key_management = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const aeskw_js_1 = require_aeskw();
	const ECDH = require_ecdhes();
	const pbes2kw_js_1 = require_pbes2kw();
	const rsaes_js_1 = require_rsaes();
	const base64url_js_1 = require_base64url$1();
	const normalize_key_js_1 = require_normalize_key();
	const errors_js_1 = require_errors();
	const cek_js_1 = require_cek();
	const import_js_1 = require_import();
	const check_key_type_js_1 = require_check_key_type();
	const is_object_js_1 = require_is_object();
	const aesgcmkw_js_1 = require_aesgcmkw();
	async function decryptKeyManagement(alg, key, encryptedKey, joseHeader, options) {
		(0, check_key_type_js_1.default)(alg, key, "decrypt");
		key = await normalize_key_js_1.default.normalizePrivateKey?.(key, alg) || key;
		switch (alg) {
			case "dir":
				if (encryptedKey !== void 0) throw new errors_js_1.JWEInvalid("Encountered unexpected JWE Encrypted Key");
				return key;
			case "ECDH-ES": if (encryptedKey !== void 0) throw new errors_js_1.JWEInvalid("Encountered unexpected JWE Encrypted Key");
			case "ECDH-ES+A128KW":
			case "ECDH-ES+A192KW":
			case "ECDH-ES+A256KW": {
				if (!(0, is_object_js_1.default)(joseHeader.epk)) throw new errors_js_1.JWEInvalid(`JOSE Header "epk" (Ephemeral Public Key) missing or invalid`);
				if (!ECDH.ecdhAllowed(key)) throw new errors_js_1.JOSENotSupported("ECDH with the provided key is not allowed or not supported by your javascript runtime");
				const epk = await (0, import_js_1.importJWK)(joseHeader.epk, alg);
				let partyUInfo;
				let partyVInfo;
				if (joseHeader.apu !== void 0) {
					if (typeof joseHeader.apu !== "string") throw new errors_js_1.JWEInvalid(`JOSE Header "apu" (Agreement PartyUInfo) invalid`);
					try {
						partyUInfo = (0, base64url_js_1.decode)(joseHeader.apu);
					} catch {
						throw new errors_js_1.JWEInvalid("Failed to base64url decode the apu");
					}
				}
				if (joseHeader.apv !== void 0) {
					if (typeof joseHeader.apv !== "string") throw new errors_js_1.JWEInvalid(`JOSE Header "apv" (Agreement PartyVInfo) invalid`);
					try {
						partyVInfo = (0, base64url_js_1.decode)(joseHeader.apv);
					} catch {
						throw new errors_js_1.JWEInvalid("Failed to base64url decode the apv");
					}
				}
				const sharedSecret = await ECDH.deriveKey(epk, key, alg === "ECDH-ES" ? joseHeader.enc : alg, alg === "ECDH-ES" ? (0, cek_js_1.bitLength)(joseHeader.enc) : parseInt(alg.slice(-5, -2), 10), partyUInfo, partyVInfo);
				if (alg === "ECDH-ES") return sharedSecret;
				if (encryptedKey === void 0) throw new errors_js_1.JWEInvalid("JWE Encrypted Key missing");
				return (0, aeskw_js_1.unwrap)(alg.slice(-6), sharedSecret, encryptedKey);
			}
			case "RSA1_5":
			case "RSA-OAEP":
			case "RSA-OAEP-256":
			case "RSA-OAEP-384":
			case "RSA-OAEP-512":
				if (encryptedKey === void 0) throw new errors_js_1.JWEInvalid("JWE Encrypted Key missing");
				return (0, rsaes_js_1.decrypt)(alg, key, encryptedKey);
			case "PBES2-HS256+A128KW":
			case "PBES2-HS384+A192KW":
			case "PBES2-HS512+A256KW": {
				if (encryptedKey === void 0) throw new errors_js_1.JWEInvalid("JWE Encrypted Key missing");
				if (typeof joseHeader.p2c !== "number") throw new errors_js_1.JWEInvalid(`JOSE Header "p2c" (PBES2 Count) missing or invalid`);
				const p2cLimit = options?.maxPBES2Count || 1e4;
				if (joseHeader.p2c > p2cLimit) throw new errors_js_1.JWEInvalid(`JOSE Header "p2c" (PBES2 Count) out is of acceptable bounds`);
				if (typeof joseHeader.p2s !== "string") throw new errors_js_1.JWEInvalid(`JOSE Header "p2s" (PBES2 Salt) missing or invalid`);
				let p2s;
				try {
					p2s = (0, base64url_js_1.decode)(joseHeader.p2s);
				} catch {
					throw new errors_js_1.JWEInvalid("Failed to base64url decode the p2s");
				}
				return (0, pbes2kw_js_1.decrypt)(alg, key, encryptedKey, joseHeader.p2c, p2s);
			}
			case "A128KW":
			case "A192KW":
			case "A256KW":
				if (encryptedKey === void 0) throw new errors_js_1.JWEInvalid("JWE Encrypted Key missing");
				return (0, aeskw_js_1.unwrap)(alg, key, encryptedKey);
			case "A128GCMKW":
			case "A192GCMKW":
			case "A256GCMKW": {
				if (encryptedKey === void 0) throw new errors_js_1.JWEInvalid("JWE Encrypted Key missing");
				if (typeof joseHeader.iv !== "string") throw new errors_js_1.JWEInvalid(`JOSE Header "iv" (Initialization Vector) missing or invalid`);
				if (typeof joseHeader.tag !== "string") throw new errors_js_1.JWEInvalid(`JOSE Header "tag" (Authentication Tag) missing or invalid`);
				let iv;
				try {
					iv = (0, base64url_js_1.decode)(joseHeader.iv);
				} catch {
					throw new errors_js_1.JWEInvalid("Failed to base64url decode the iv");
				}
				let tag;
				try {
					tag = (0, base64url_js_1.decode)(joseHeader.tag);
				} catch {
					throw new errors_js_1.JWEInvalid("Failed to base64url decode the tag");
				}
				return (0, aesgcmkw_js_1.unwrap)(alg, key, encryptedKey, iv, tag);
			}
			default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported \"alg\" (JWE Algorithm) header value");
		}
	}
	exports.default = decryptKeyManagement;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/validate_crit.js
var require_validate_crit = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const errors_js_1 = require_errors();
	function validateCrit(Err, recognizedDefault, recognizedOption, protectedHeader, joseHeader) {
		if (joseHeader.crit !== void 0 && protectedHeader?.crit === void 0) throw new Err("\"crit\" (Critical) Header Parameter MUST be integrity protected");
		if (!protectedHeader || protectedHeader.crit === void 0) return /* @__PURE__ */ new Set();
		if (!Array.isArray(protectedHeader.crit) || protectedHeader.crit.length === 0 || protectedHeader.crit.some((input) => typeof input !== "string" || input.length === 0)) throw new Err("\"crit\" (Critical) Header Parameter MUST be an array of non-empty strings when present");
		let recognized;
		if (recognizedOption !== void 0) recognized = new Map([...Object.entries(recognizedOption), ...recognizedDefault.entries()]);
		else recognized = recognizedDefault;
		for (const parameter of protectedHeader.crit) {
			if (!recognized.has(parameter)) throw new errors_js_1.JOSENotSupported(`Extension Header Parameter "${parameter}" is not recognized`);
			if (joseHeader[parameter] === void 0) throw new Err(`Extension Header Parameter "${parameter}" is missing`);
			if (recognized.get(parameter) && protectedHeader[parameter] === void 0) throw new Err(`Extension Header Parameter "${parameter}" MUST be integrity protected`);
		}
		return new Set(protectedHeader.crit);
	}
	exports.default = validateCrit;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/validate_algorithms.js
var require_validate_algorithms = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const validateAlgorithms = (option, algorithms) => {
		if (algorithms !== void 0 && (!Array.isArray(algorithms) || algorithms.some((s) => typeof s !== "string"))) throw new TypeError(`"${option}" option must be an array of strings`);
		if (!algorithms) return;
		return new Set(algorithms);
	};
	exports.default = validateAlgorithms;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/flattened/decrypt.js
var require_decrypt$3 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.flattenedDecrypt = flattenedDecrypt;
	const base64url_js_1 = require_base64url$1();
	const decrypt_js_1 = require_decrypt$4();
	const errors_js_1 = require_errors();
	const is_disjoint_js_1 = require_is_disjoint();
	const is_object_js_1 = require_is_object();
	const decrypt_key_management_js_1 = require_decrypt_key_management();
	const buffer_utils_js_1 = require_buffer_utils();
	const cek_js_1 = require_cek();
	const validate_crit_js_1 = require_validate_crit();
	const validate_algorithms_js_1 = require_validate_algorithms();
	async function flattenedDecrypt(jwe, key, options) {
		if (!(0, is_object_js_1.default)(jwe)) throw new errors_js_1.JWEInvalid("Flattened JWE must be an object");
		if (jwe.protected === void 0 && jwe.header === void 0 && jwe.unprotected === void 0) throw new errors_js_1.JWEInvalid("JOSE Header missing");
		if (jwe.iv !== void 0 && typeof jwe.iv !== "string") throw new errors_js_1.JWEInvalid("JWE Initialization Vector incorrect type");
		if (typeof jwe.ciphertext !== "string") throw new errors_js_1.JWEInvalid("JWE Ciphertext missing or incorrect type");
		if (jwe.tag !== void 0 && typeof jwe.tag !== "string") throw new errors_js_1.JWEInvalid("JWE Authentication Tag incorrect type");
		if (jwe.protected !== void 0 && typeof jwe.protected !== "string") throw new errors_js_1.JWEInvalid("JWE Protected Header incorrect type");
		if (jwe.encrypted_key !== void 0 && typeof jwe.encrypted_key !== "string") throw new errors_js_1.JWEInvalid("JWE Encrypted Key incorrect type");
		if (jwe.aad !== void 0 && typeof jwe.aad !== "string") throw new errors_js_1.JWEInvalid("JWE AAD incorrect type");
		if (jwe.header !== void 0 && !(0, is_object_js_1.default)(jwe.header)) throw new errors_js_1.JWEInvalid("JWE Shared Unprotected Header incorrect type");
		if (jwe.unprotected !== void 0 && !(0, is_object_js_1.default)(jwe.unprotected)) throw new errors_js_1.JWEInvalid("JWE Per-Recipient Unprotected Header incorrect type");
		let parsedProt;
		if (jwe.protected) try {
			const protectedHeader = (0, base64url_js_1.decode)(jwe.protected);
			parsedProt = JSON.parse(buffer_utils_js_1.decoder.decode(protectedHeader));
		} catch {
			throw new errors_js_1.JWEInvalid("JWE Protected Header is invalid");
		}
		if (!(0, is_disjoint_js_1.default)(parsedProt, jwe.header, jwe.unprotected)) throw new errors_js_1.JWEInvalid("JWE Protected, JWE Unprotected Header, and JWE Per-Recipient Unprotected Header Parameter names must be disjoint");
		const joseHeader = {
			...parsedProt,
			...jwe.header,
			...jwe.unprotected
		};
		(0, validate_crit_js_1.default)(errors_js_1.JWEInvalid, /* @__PURE__ */ new Map(), options?.crit, parsedProt, joseHeader);
		if (joseHeader.zip !== void 0) throw new errors_js_1.JOSENotSupported("JWE \"zip\" (Compression Algorithm) Header Parameter is not supported.");
		const { alg, enc } = joseHeader;
		if (typeof alg !== "string" || !alg) throw new errors_js_1.JWEInvalid("missing JWE Algorithm (alg) in JWE Header");
		if (typeof enc !== "string" || !enc) throw new errors_js_1.JWEInvalid("missing JWE Encryption Algorithm (enc) in JWE Header");
		const keyManagementAlgorithms = options && (0, validate_algorithms_js_1.default)("keyManagementAlgorithms", options.keyManagementAlgorithms);
		const contentEncryptionAlgorithms = options && (0, validate_algorithms_js_1.default)("contentEncryptionAlgorithms", options.contentEncryptionAlgorithms);
		if (keyManagementAlgorithms && !keyManagementAlgorithms.has(alg) || !keyManagementAlgorithms && alg.startsWith("PBES2")) throw new errors_js_1.JOSEAlgNotAllowed("\"alg\" (Algorithm) Header Parameter value not allowed");
		if (contentEncryptionAlgorithms && !contentEncryptionAlgorithms.has(enc)) throw new errors_js_1.JOSEAlgNotAllowed("\"enc\" (Encryption Algorithm) Header Parameter value not allowed");
		let encryptedKey;
		if (jwe.encrypted_key !== void 0) try {
			encryptedKey = (0, base64url_js_1.decode)(jwe.encrypted_key);
		} catch {
			throw new errors_js_1.JWEInvalid("Failed to base64url decode the encrypted_key");
		}
		let resolvedKey = false;
		if (typeof key === "function") {
			key = await key(parsedProt, jwe);
			resolvedKey = true;
		}
		let cek;
		try {
			cek = await (0, decrypt_key_management_js_1.default)(alg, key, encryptedKey, joseHeader, options);
		} catch (err) {
			if (err instanceof TypeError || err instanceof errors_js_1.JWEInvalid || err instanceof errors_js_1.JOSENotSupported) throw err;
			cek = (0, cek_js_1.default)(enc);
		}
		let iv;
		let tag;
		if (jwe.iv !== void 0) try {
			iv = (0, base64url_js_1.decode)(jwe.iv);
		} catch {
			throw new errors_js_1.JWEInvalid("Failed to base64url decode the iv");
		}
		if (jwe.tag !== void 0) try {
			tag = (0, base64url_js_1.decode)(jwe.tag);
		} catch {
			throw new errors_js_1.JWEInvalid("Failed to base64url decode the tag");
		}
		const protectedHeader = buffer_utils_js_1.encoder.encode(jwe.protected ?? "");
		let additionalData;
		if (jwe.aad !== void 0) additionalData = (0, buffer_utils_js_1.concat)(protectedHeader, buffer_utils_js_1.encoder.encode("."), buffer_utils_js_1.encoder.encode(jwe.aad));
		else additionalData = protectedHeader;
		let ciphertext;
		try {
			ciphertext = (0, base64url_js_1.decode)(jwe.ciphertext);
		} catch {
			throw new errors_js_1.JWEInvalid("Failed to base64url decode the ciphertext");
		}
		const result = { plaintext: await (0, decrypt_js_1.default)(enc, cek, ciphertext, iv, tag, additionalData) };
		if (jwe.protected !== void 0) result.protectedHeader = parsedProt;
		if (jwe.aad !== void 0) try {
			result.additionalAuthenticatedData = (0, base64url_js_1.decode)(jwe.aad);
		} catch {
			throw new errors_js_1.JWEInvalid("Failed to base64url decode the aad");
		}
		if (jwe.unprotected !== void 0) result.sharedUnprotectedHeader = jwe.unprotected;
		if (jwe.header !== void 0) result.unprotectedHeader = jwe.header;
		if (resolvedKey) return {
			...result,
			key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/compact/decrypt.js
var require_decrypt$2 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.compactDecrypt = compactDecrypt;
	const decrypt_js_1 = require_decrypt$3();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	async function compactDecrypt(jwe, key, options) {
		if (jwe instanceof Uint8Array) jwe = buffer_utils_js_1.decoder.decode(jwe);
		if (typeof jwe !== "string") throw new errors_js_1.JWEInvalid("Compact JWE must be a string or Uint8Array");
		const { 0: protectedHeader, 1: encryptedKey, 2: iv, 3: ciphertext, 4: tag, length } = jwe.split(".");
		if (length !== 5) throw new errors_js_1.JWEInvalid("Invalid Compact JWE");
		const decrypted = await (0, decrypt_js_1.flattenedDecrypt)({
			ciphertext,
			iv: iv || void 0,
			protected: protectedHeader,
			tag: tag || void 0,
			encrypted_key: encryptedKey || void 0
		}, key, options);
		const result = {
			plaintext: decrypted.plaintext,
			protectedHeader: decrypted.protectedHeader
		};
		if (typeof key === "function") return {
			...result,
			key: decrypted.key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/general/decrypt.js
var require_decrypt$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.generalDecrypt = generalDecrypt;
	const decrypt_js_1 = require_decrypt$3();
	const errors_js_1 = require_errors();
	const is_object_js_1 = require_is_object();
	async function generalDecrypt(jwe, key, options) {
		if (!(0, is_object_js_1.default)(jwe)) throw new errors_js_1.JWEInvalid("General JWE must be an object");
		if (!Array.isArray(jwe.recipients) || !jwe.recipients.every(is_object_js_1.default)) throw new errors_js_1.JWEInvalid("JWE Recipients missing or incorrect type");
		if (!jwe.recipients.length) throw new errors_js_1.JWEInvalid("JWE Recipients has no members");
		for (const recipient of jwe.recipients) try {
			return await (0, decrypt_js_1.flattenedDecrypt)({
				aad: jwe.aad,
				ciphertext: jwe.ciphertext,
				encrypted_key: recipient.encrypted_key,
				header: recipient.header,
				iv: jwe.iv,
				protected: jwe.protected,
				tag: jwe.tag,
				unprotected: jwe.unprotected
			}, key, options);
		} catch {}
		throw new errors_js_1.JWEDecryptionFailed();
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/private_symbols.js
var require_private_symbols = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.unprotected = void 0;
	exports.unprotected = Symbol();
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/key_to_jwk.js
var require_key_to_jwk = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const node_crypto_1$3 = __require("node:crypto");
	const base64url_js_1 = require_base64url$1();
	const errors_js_1 = require_errors();
	const webcrypto_js_1 = require_webcrypto();
	const is_key_object_js_1 = require_is_key_object();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const keyToJWK = (key) => {
		let keyObject;
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			if (!key.extractable) throw new TypeError("CryptoKey is not extractable");
			keyObject = node_crypto_1$3.KeyObject.from(key);
		} else if ((0, is_key_object_js_1.default)(key)) keyObject = key;
		else if (key instanceof Uint8Array) return {
			kty: "oct",
			k: (0, base64url_js_1.encode)(key)
		};
		else throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types, "Uint8Array"));
		if (keyObject.type !== "secret" && ![
			"rsa",
			"ec",
			"ed25519",
			"x25519",
			"ed448",
			"x448"
		].includes(keyObject.asymmetricKeyType)) throw new errors_js_1.JOSENotSupported("Unsupported key asymmetricKeyType");
		return keyObject.export({ format: "jwk" });
	};
	exports.default = keyToJWK;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/key/export.js
var require_export = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.exportSPKI = exportSPKI;
	exports.exportPKCS8 = exportPKCS8;
	exports.exportJWK = exportJWK;
	const asn1_js_1 = require_asn1();
	const asn1_js_2 = require_asn1();
	const key_to_jwk_js_1 = require_key_to_jwk();
	async function exportSPKI(key) {
		return (0, asn1_js_1.toSPKI)(key);
	}
	async function exportPKCS8(key) {
		return (0, asn1_js_2.toPKCS8)(key);
	}
	async function exportJWK(key) {
		return (0, key_to_jwk_js_1.default)(key);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/encrypt_key_management.js
var require_encrypt_key_management = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const aeskw_js_1 = require_aeskw();
	const ECDH = require_ecdhes();
	const pbes2kw_js_1 = require_pbes2kw();
	const rsaes_js_1 = require_rsaes();
	const base64url_js_1 = require_base64url$1();
	const normalize_key_js_1 = require_normalize_key();
	const cek_js_1 = require_cek();
	const errors_js_1 = require_errors();
	const export_js_1 = require_export();
	const check_key_type_js_1 = require_check_key_type();
	const aesgcmkw_js_1 = require_aesgcmkw();
	async function encryptKeyManagement(alg, enc, key, providedCek, providedParameters = {}) {
		let encryptedKey;
		let parameters;
		let cek;
		(0, check_key_type_js_1.default)(alg, key, "encrypt");
		key = await normalize_key_js_1.default.normalizePublicKey?.(key, alg) || key;
		switch (alg) {
			case "dir":
				cek = key;
				break;
			case "ECDH-ES":
			case "ECDH-ES+A128KW":
			case "ECDH-ES+A192KW":
			case "ECDH-ES+A256KW": {
				if (!ECDH.ecdhAllowed(key)) throw new errors_js_1.JOSENotSupported("ECDH with the provided key is not allowed or not supported by your javascript runtime");
				const { apu, apv } = providedParameters;
				let { epk: ephemeralKey } = providedParameters;
				ephemeralKey ||= (await ECDH.generateEpk(key)).privateKey;
				const { x, y, crv, kty } = await (0, export_js_1.exportJWK)(ephemeralKey);
				const sharedSecret = await ECDH.deriveKey(key, ephemeralKey, alg === "ECDH-ES" ? enc : alg, alg === "ECDH-ES" ? (0, cek_js_1.bitLength)(enc) : parseInt(alg.slice(-5, -2), 10), apu, apv);
				parameters = { epk: {
					x,
					crv,
					kty
				} };
				if (kty === "EC") parameters.epk.y = y;
				if (apu) parameters.apu = (0, base64url_js_1.encode)(apu);
				if (apv) parameters.apv = (0, base64url_js_1.encode)(apv);
				if (alg === "ECDH-ES") {
					cek = sharedSecret;
					break;
				}
				cek = providedCek || (0, cek_js_1.default)(enc);
				const kwAlg = alg.slice(-6);
				encryptedKey = await (0, aeskw_js_1.wrap)(kwAlg, sharedSecret, cek);
				break;
			}
			case "RSA1_5":
			case "RSA-OAEP":
			case "RSA-OAEP-256":
			case "RSA-OAEP-384":
			case "RSA-OAEP-512":
				cek = providedCek || (0, cek_js_1.default)(enc);
				encryptedKey = await (0, rsaes_js_1.encrypt)(alg, key, cek);
				break;
			case "PBES2-HS256+A128KW":
			case "PBES2-HS384+A192KW":
			case "PBES2-HS512+A256KW": {
				cek = providedCek || (0, cek_js_1.default)(enc);
				const { p2c, p2s } = providedParameters;
				({encryptedKey, ...parameters} = await (0, pbes2kw_js_1.encrypt)(alg, key, cek, p2c, p2s));
				break;
			}
			case "A128KW":
			case "A192KW":
			case "A256KW":
				cek = providedCek || (0, cek_js_1.default)(enc);
				encryptedKey = await (0, aeskw_js_1.wrap)(alg, key, cek);
				break;
			case "A128GCMKW":
			case "A192GCMKW":
			case "A256GCMKW": {
				cek = providedCek || (0, cek_js_1.default)(enc);
				const { iv } = providedParameters;
				({encryptedKey, ...parameters} = await (0, aesgcmkw_js_1.wrap)(alg, key, cek, iv));
				break;
			}
			default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported \"alg\" (JWE Algorithm) header value");
		}
		return {
			cek,
			encryptedKey,
			parameters
		};
	}
	exports.default = encryptKeyManagement;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/flattened/encrypt.js
var require_encrypt$3 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.FlattenedEncrypt = void 0;
	const base64url_js_1 = require_base64url$1();
	const private_symbols_js_1 = require_private_symbols();
	const encrypt_js_1 = require_encrypt$4();
	const encrypt_key_management_js_1 = require_encrypt_key_management();
	const errors_js_1 = require_errors();
	const is_disjoint_js_1 = require_is_disjoint();
	const buffer_utils_js_1 = require_buffer_utils();
	const validate_crit_js_1 = require_validate_crit();
	var FlattenedEncrypt = class {
		_plaintext;
		_protectedHeader;
		_sharedUnprotectedHeader;
		_unprotectedHeader;
		_aad;
		_cek;
		_iv;
		_keyManagementParameters;
		constructor(plaintext) {
			if (!(plaintext instanceof Uint8Array)) throw new TypeError("plaintext must be an instance of Uint8Array");
			this._plaintext = plaintext;
		}
		setKeyManagementParameters(parameters) {
			if (this._keyManagementParameters) throw new TypeError("setKeyManagementParameters can only be called once");
			this._keyManagementParameters = parameters;
			return this;
		}
		setProtectedHeader(protectedHeader) {
			if (this._protectedHeader) throw new TypeError("setProtectedHeader can only be called once");
			this._protectedHeader = protectedHeader;
			return this;
		}
		setSharedUnprotectedHeader(sharedUnprotectedHeader) {
			if (this._sharedUnprotectedHeader) throw new TypeError("setSharedUnprotectedHeader can only be called once");
			this._sharedUnprotectedHeader = sharedUnprotectedHeader;
			return this;
		}
		setUnprotectedHeader(unprotectedHeader) {
			if (this._unprotectedHeader) throw new TypeError("setUnprotectedHeader can only be called once");
			this._unprotectedHeader = unprotectedHeader;
			return this;
		}
		setAdditionalAuthenticatedData(aad) {
			this._aad = aad;
			return this;
		}
		setContentEncryptionKey(cek) {
			if (this._cek) throw new TypeError("setContentEncryptionKey can only be called once");
			this._cek = cek;
			return this;
		}
		setInitializationVector(iv) {
			if (this._iv) throw new TypeError("setInitializationVector can only be called once");
			this._iv = iv;
			return this;
		}
		async encrypt(key, options) {
			if (!this._protectedHeader && !this._unprotectedHeader && !this._sharedUnprotectedHeader) throw new errors_js_1.JWEInvalid("either setProtectedHeader, setUnprotectedHeader, or sharedUnprotectedHeader must be called before #encrypt()");
			if (!(0, is_disjoint_js_1.default)(this._protectedHeader, this._unprotectedHeader, this._sharedUnprotectedHeader)) throw new errors_js_1.JWEInvalid("JWE Protected, JWE Shared Unprotected and JWE Per-Recipient Header Parameter names must be disjoint");
			const joseHeader = {
				...this._protectedHeader,
				...this._unprotectedHeader,
				...this._sharedUnprotectedHeader
			};
			(0, validate_crit_js_1.default)(errors_js_1.JWEInvalid, /* @__PURE__ */ new Map(), options?.crit, this._protectedHeader, joseHeader);
			if (joseHeader.zip !== void 0) throw new errors_js_1.JOSENotSupported("JWE \"zip\" (Compression Algorithm) Header Parameter is not supported.");
			const { alg, enc } = joseHeader;
			if (typeof alg !== "string" || !alg) throw new errors_js_1.JWEInvalid("JWE \"alg\" (Algorithm) Header Parameter missing or invalid");
			if (typeof enc !== "string" || !enc) throw new errors_js_1.JWEInvalid("JWE \"enc\" (Encryption Algorithm) Header Parameter missing or invalid");
			let encryptedKey;
			if (this._cek && (alg === "dir" || alg === "ECDH-ES")) throw new TypeError(`setContentEncryptionKey cannot be called with JWE "alg" (Algorithm) Header ${alg}`);
			let cek;
			{
				let parameters;
				({cek, encryptedKey, parameters} = await (0, encrypt_key_management_js_1.default)(alg, enc, key, this._cek, this._keyManagementParameters));
				if (parameters) {
					if (options && private_symbols_js_1.unprotected in options) {
						if (!this._unprotectedHeader) this.setUnprotectedHeader(parameters);
						else this._unprotectedHeader = {
							...this._unprotectedHeader,
							...parameters
						};
					} else if (!this._protectedHeader) this.setProtectedHeader(parameters);
					else this._protectedHeader = {
						...this._protectedHeader,
						...parameters
					};
				}
			}
			let additionalData;
			let protectedHeader;
			let aadMember;
			if (this._protectedHeader) protectedHeader = buffer_utils_js_1.encoder.encode((0, base64url_js_1.encode)(JSON.stringify(this._protectedHeader)));
			else protectedHeader = buffer_utils_js_1.encoder.encode("");
			if (this._aad) {
				aadMember = (0, base64url_js_1.encode)(this._aad);
				additionalData = (0, buffer_utils_js_1.concat)(protectedHeader, buffer_utils_js_1.encoder.encode("."), buffer_utils_js_1.encoder.encode(aadMember));
			} else additionalData = protectedHeader;
			const { ciphertext, tag, iv } = await (0, encrypt_js_1.default)(enc, this._plaintext, cek, this._iv, additionalData);
			const jwe = { ciphertext: (0, base64url_js_1.encode)(ciphertext) };
			if (iv) jwe.iv = (0, base64url_js_1.encode)(iv);
			if (tag) jwe.tag = (0, base64url_js_1.encode)(tag);
			if (encryptedKey) jwe.encrypted_key = (0, base64url_js_1.encode)(encryptedKey);
			if (aadMember) jwe.aad = aadMember;
			if (this._protectedHeader) jwe.protected = buffer_utils_js_1.decoder.decode(protectedHeader);
			if (this._sharedUnprotectedHeader) jwe.unprotected = this._sharedUnprotectedHeader;
			if (this._unprotectedHeader) jwe.header = this._unprotectedHeader;
			return jwe;
		}
	};
	exports.FlattenedEncrypt = FlattenedEncrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/general/encrypt.js
var require_encrypt$2 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.GeneralEncrypt = void 0;
	const encrypt_js_1 = require_encrypt$3();
	const private_symbols_js_1 = require_private_symbols();
	const errors_js_1 = require_errors();
	const cek_js_1 = require_cek();
	const is_disjoint_js_1 = require_is_disjoint();
	const encrypt_key_management_js_1 = require_encrypt_key_management();
	const base64url_js_1 = require_base64url$1();
	const validate_crit_js_1 = require_validate_crit();
	var IndividualRecipient = class {
		parent;
		unprotectedHeader;
		key;
		options;
		constructor(enc, key, options) {
			this.parent = enc;
			this.key = key;
			this.options = options;
		}
		setUnprotectedHeader(unprotectedHeader) {
			if (this.unprotectedHeader) throw new TypeError("setUnprotectedHeader can only be called once");
			this.unprotectedHeader = unprotectedHeader;
			return this;
		}
		addRecipient(...args) {
			return this.parent.addRecipient(...args);
		}
		encrypt(...args) {
			return this.parent.encrypt(...args);
		}
		done() {
			return this.parent;
		}
	};
	var GeneralEncrypt = class {
		_plaintext;
		_recipients = [];
		_protectedHeader;
		_unprotectedHeader;
		_aad;
		constructor(plaintext) {
			this._plaintext = plaintext;
		}
		addRecipient(key, options) {
			const recipient = new IndividualRecipient(this, key, { crit: options?.crit });
			this._recipients.push(recipient);
			return recipient;
		}
		setProtectedHeader(protectedHeader) {
			if (this._protectedHeader) throw new TypeError("setProtectedHeader can only be called once");
			this._protectedHeader = protectedHeader;
			return this;
		}
		setSharedUnprotectedHeader(sharedUnprotectedHeader) {
			if (this._unprotectedHeader) throw new TypeError("setSharedUnprotectedHeader can only be called once");
			this._unprotectedHeader = sharedUnprotectedHeader;
			return this;
		}
		setAdditionalAuthenticatedData(aad) {
			this._aad = aad;
			return this;
		}
		async encrypt() {
			if (!this._recipients.length) throw new errors_js_1.JWEInvalid("at least one recipient must be added");
			if (this._recipients.length === 1) {
				const [recipient] = this._recipients;
				const flattened = await new encrypt_js_1.FlattenedEncrypt(this._plaintext).setAdditionalAuthenticatedData(this._aad).setProtectedHeader(this._protectedHeader).setSharedUnprotectedHeader(this._unprotectedHeader).setUnprotectedHeader(recipient.unprotectedHeader).encrypt(recipient.key, { ...recipient.options });
				const jwe = {
					ciphertext: flattened.ciphertext,
					iv: flattened.iv,
					recipients: [{}],
					tag: flattened.tag
				};
				if (flattened.aad) jwe.aad = flattened.aad;
				if (flattened.protected) jwe.protected = flattened.protected;
				if (flattened.unprotected) jwe.unprotected = flattened.unprotected;
				if (flattened.encrypted_key) jwe.recipients[0].encrypted_key = flattened.encrypted_key;
				if (flattened.header) jwe.recipients[0].header = flattened.header;
				return jwe;
			}
			let enc;
			for (let i = 0; i < this._recipients.length; i++) {
				const recipient = this._recipients[i];
				if (!(0, is_disjoint_js_1.default)(this._protectedHeader, this._unprotectedHeader, recipient.unprotectedHeader)) throw new errors_js_1.JWEInvalid("JWE Protected, JWE Shared Unprotected and JWE Per-Recipient Header Parameter names must be disjoint");
				const joseHeader = {
					...this._protectedHeader,
					...this._unprotectedHeader,
					...recipient.unprotectedHeader
				};
				const { alg } = joseHeader;
				if (typeof alg !== "string" || !alg) throw new errors_js_1.JWEInvalid("JWE \"alg\" (Algorithm) Header Parameter missing or invalid");
				if (alg === "dir" || alg === "ECDH-ES") throw new errors_js_1.JWEInvalid("\"dir\" and \"ECDH-ES\" alg may only be used with a single recipient");
				if (typeof joseHeader.enc !== "string" || !joseHeader.enc) throw new errors_js_1.JWEInvalid("JWE \"enc\" (Encryption Algorithm) Header Parameter missing or invalid");
				if (!enc) enc = joseHeader.enc;
				else if (enc !== joseHeader.enc) throw new errors_js_1.JWEInvalid("JWE \"enc\" (Encryption Algorithm) Header Parameter must be the same for all recipients");
				(0, validate_crit_js_1.default)(errors_js_1.JWEInvalid, /* @__PURE__ */ new Map(), recipient.options.crit, this._protectedHeader, joseHeader);
				if (joseHeader.zip !== void 0) throw new errors_js_1.JOSENotSupported("JWE \"zip\" (Compression Algorithm) Header Parameter is not supported.");
			}
			const cek = (0, cek_js_1.default)(enc);
			const jwe = {
				ciphertext: "",
				iv: "",
				recipients: [],
				tag: ""
			};
			for (let i = 0; i < this._recipients.length; i++) {
				const recipient = this._recipients[i];
				const target = {};
				jwe.recipients.push(target);
				const p2c = {
					...this._protectedHeader,
					...this._unprotectedHeader,
					...recipient.unprotectedHeader
				}.alg.startsWith("PBES2") ? 2048 + i : void 0;
				if (i === 0) {
					const flattened = await new encrypt_js_1.FlattenedEncrypt(this._plaintext).setAdditionalAuthenticatedData(this._aad).setContentEncryptionKey(cek).setProtectedHeader(this._protectedHeader).setSharedUnprotectedHeader(this._unprotectedHeader).setUnprotectedHeader(recipient.unprotectedHeader).setKeyManagementParameters({ p2c }).encrypt(recipient.key, {
						...recipient.options,
						[private_symbols_js_1.unprotected]: true
					});
					jwe.ciphertext = flattened.ciphertext;
					jwe.iv = flattened.iv;
					jwe.tag = flattened.tag;
					if (flattened.aad) jwe.aad = flattened.aad;
					if (flattened.protected) jwe.protected = flattened.protected;
					if (flattened.unprotected) jwe.unprotected = flattened.unprotected;
					target.encrypted_key = flattened.encrypted_key;
					if (flattened.header) target.header = flattened.header;
					continue;
				}
				const { encryptedKey, parameters } = await (0, encrypt_key_management_js_1.default)(recipient.unprotectedHeader?.alg || this._protectedHeader?.alg || this._unprotectedHeader?.alg, enc, recipient.key, cek, { p2c });
				target.encrypted_key = (0, base64url_js_1.encode)(encryptedKey);
				if (recipient.unprotectedHeader || parameters) target.header = {
					...recipient.unprotectedHeader,
					...parameters
				};
			}
			return jwe;
		}
	};
	exports.GeneralEncrypt = GeneralEncrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/dsa_digest.js
var require_dsa_digest = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = dsaDigest;
	const errors_js_1 = require_errors();
	function dsaDigest(alg) {
		switch (alg) {
			case "PS256":
			case "RS256":
			case "ES256":
			case "ES256K": return "sha256";
			case "PS384":
			case "RS384":
			case "ES384": return "sha384";
			case "PS512":
			case "RS512":
			case "ES512": return "sha512";
			case "Ed25519":
			case "EdDSA": return;
			default: throw new errors_js_1.JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
		}
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/node_key.js
var require_node_key = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = keyForCrypto;
	const node_crypto_1$2 = __require("node:crypto");
	const get_named_curve_js_1 = require_get_named_curve();
	const errors_js_1 = require_errors();
	const check_key_length_js_1 = require_check_key_length();
	const ecCurveAlgMap = /* @__PURE__ */ new Map([
		["ES256", "P-256"],
		["ES256K", "secp256k1"],
		["ES384", "P-384"],
		["ES512", "P-521"]
	]);
	function keyForCrypto(alg, key) {
		let asymmetricKeyType;
		let asymmetricKeyDetails;
		let isJWK;
		if (key instanceof node_crypto_1$2.KeyObject) {
			asymmetricKeyType = key.asymmetricKeyType;
			asymmetricKeyDetails = key.asymmetricKeyDetails;
		} else {
			isJWK = true;
			switch (key.kty) {
				case "RSA":
					asymmetricKeyType = "rsa";
					break;
				case "EC":
					asymmetricKeyType = "ec";
					break;
				case "OKP":
					if (key.crv === "Ed25519") {
						asymmetricKeyType = "ed25519";
						break;
					}
					if (key.crv === "Ed448") {
						asymmetricKeyType = "ed448";
						break;
					}
					throw new TypeError("Invalid key for this operation, its crv must be Ed25519 or Ed448");
				default: throw new TypeError("Invalid key for this operation, its kty must be RSA, OKP, or EC");
			}
		}
		let options;
		switch (alg) {
			case "Ed25519":
				if (asymmetricKeyType !== "ed25519") throw new TypeError(`Invalid key for this operation, its asymmetricKeyType must be ed25519`);
				break;
			case "EdDSA":
				if (!["ed25519", "ed448"].includes(asymmetricKeyType)) throw new TypeError("Invalid key for this operation, its asymmetricKeyType must be ed25519 or ed448");
				break;
			case "RS256":
			case "RS384":
			case "RS512":
				if (asymmetricKeyType !== "rsa") throw new TypeError("Invalid key for this operation, its asymmetricKeyType must be rsa");
				(0, check_key_length_js_1.default)(key, alg);
				break;
			case "PS256":
			case "PS384":
			case "PS512":
				if (asymmetricKeyType === "rsa-pss") {
					const { hashAlgorithm, mgf1HashAlgorithm, saltLength } = asymmetricKeyDetails;
					const length = parseInt(alg.slice(-3), 10);
					if (hashAlgorithm !== void 0 && (hashAlgorithm !== `sha${length}` || mgf1HashAlgorithm !== hashAlgorithm)) throw new TypeError(`Invalid key for this operation, its RSA-PSS parameters do not meet the requirements of "alg" ${alg}`);
					if (saltLength !== void 0 && saltLength > length >> 3) throw new TypeError(`Invalid key for this operation, its RSA-PSS parameter saltLength does not meet the requirements of "alg" ${alg}`);
				} else if (asymmetricKeyType !== "rsa") throw new TypeError("Invalid key for this operation, its asymmetricKeyType must be rsa or rsa-pss");
				(0, check_key_length_js_1.default)(key, alg);
				options = {
					padding: node_crypto_1$2.constants.RSA_PKCS1_PSS_PADDING,
					saltLength: node_crypto_1$2.constants.RSA_PSS_SALTLEN_DIGEST
				};
				break;
			case "ES256":
			case "ES256K":
			case "ES384":
			case "ES512": {
				if (asymmetricKeyType !== "ec") throw new TypeError("Invalid key for this operation, its asymmetricKeyType must be ec");
				const actual = (0, get_named_curve_js_1.default)(key);
				const expected = ecCurveAlgMap.get(alg);
				if (actual !== expected) throw new TypeError(`Invalid key curve for the algorithm, its curve must be ${expected}, got ${actual}`);
				options = { dsaEncoding: "ieee-p1363" };
				break;
			}
			default: throw new errors_js_1.JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
		}
		if (isJWK) return {
			format: "jwk",
			key,
			...options
		};
		return options ? {
			...options,
			key
		} : key;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/hmac_digest.js
var require_hmac_digest = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = hmacDigest;
	const errors_js_1 = require_errors();
	function hmacDigest(alg) {
		switch (alg) {
			case "HS256": return "sha256";
			case "HS384": return "sha384";
			case "HS512": return "sha512";
			default: throw new errors_js_1.JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
		}
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/get_sign_verify_key.js
var require_get_sign_verify_key = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = getSignVerifyKey;
	const node_crypto_1$1 = __require("node:crypto");
	const webcrypto_js_1 = require_webcrypto();
	const crypto_key_js_1 = require_crypto_key();
	const invalid_key_input_js_1 = require_invalid_key_input();
	const is_key_like_js_1 = require_is_key_like();
	const jwk = require_is_jwk();
	function getSignVerifyKey(alg, key, usage) {
		if (key instanceof Uint8Array) {
			if (!alg.startsWith("HS")) throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types));
			return (0, node_crypto_1$1.createSecretKey)(key);
		}
		if (key instanceof node_crypto_1$1.KeyObject) return key;
		if ((0, webcrypto_js_1.isCryptoKey)(key)) {
			(0, crypto_key_js_1.checkSigCryptoKey)(key, alg, usage);
			return node_crypto_1$1.KeyObject.from(key);
		}
		if (jwk.isJWK(key)) {
			if (alg.startsWith("HS")) return (0, node_crypto_1$1.createSecretKey)(Buffer.from(key.k, "base64url"));
			return key;
		}
		throw new TypeError((0, invalid_key_input_js_1.default)(key, ...is_key_like_js_1.types, "Uint8Array", "JSON Web Key"));
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/sign.js
var require_sign$4 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const crypto$2 = __require("node:crypto");
	const node_util_1$2 = __require("node:util");
	const dsa_digest_js_1 = require_dsa_digest();
	const hmac_digest_js_1 = require_hmac_digest();
	const node_key_js_1 = require_node_key();
	const get_sign_verify_key_js_1 = require_get_sign_verify_key();
	const oneShotSign = (0, node_util_1$2.promisify)(crypto$2.sign);
	const sign = async (alg, key, data) => {
		const k = (0, get_sign_verify_key_js_1.default)(alg, key, "sign");
		if (alg.startsWith("HS")) {
			const hmac = crypto$2.createHmac((0, hmac_digest_js_1.default)(alg), k);
			hmac.update(data);
			return hmac.digest();
		}
		return oneShotSign((0, dsa_digest_js_1.default)(alg), data, (0, node_key_js_1.default)(alg, k));
	};
	exports.default = sign;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/verify.js
var require_verify$4 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const crypto$1 = __require("node:crypto");
	const node_util_1$1 = __require("node:util");
	const dsa_digest_js_1 = require_dsa_digest();
	const node_key_js_1 = require_node_key();
	const sign_js_1 = require_sign$4();
	const get_sign_verify_key_js_1 = require_get_sign_verify_key();
	const oneShotVerify = (0, node_util_1$1.promisify)(crypto$1.verify);
	const verify = async (alg, key, signature, data) => {
		const k = (0, get_sign_verify_key_js_1.default)(alg, key, "verify");
		if (alg.startsWith("HS")) {
			const expected = await (0, sign_js_1.default)(alg, k, data);
			const actual = signature;
			try {
				return crypto$1.timingSafeEqual(actual, expected);
			} catch {
				return false;
			}
		}
		const algorithm = (0, dsa_digest_js_1.default)(alg);
		const keyInput = (0, node_key_js_1.default)(alg, k);
		try {
			return await oneShotVerify(algorithm, data, keyInput, signature);
		} catch {
			return false;
		}
	};
	exports.default = verify;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/flattened/verify.js
var require_verify$3 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.flattenedVerify = flattenedVerify;
	const base64url_js_1 = require_base64url$1();
	const verify_js_1 = require_verify$4();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const is_disjoint_js_1 = require_is_disjoint();
	const is_object_js_1 = require_is_object();
	const check_key_type_js_1 = require_check_key_type();
	const validate_crit_js_1 = require_validate_crit();
	const validate_algorithms_js_1 = require_validate_algorithms();
	const is_jwk_js_1 = require_is_jwk();
	const import_js_1 = require_import();
	async function flattenedVerify(jws, key, options) {
		if (!(0, is_object_js_1.default)(jws)) throw new errors_js_1.JWSInvalid("Flattened JWS must be an object");
		if (jws.protected === void 0 && jws.header === void 0) throw new errors_js_1.JWSInvalid("Flattened JWS must have either of the \"protected\" or \"header\" members");
		if (jws.protected !== void 0 && typeof jws.protected !== "string") throw new errors_js_1.JWSInvalid("JWS Protected Header incorrect type");
		if (jws.payload === void 0) throw new errors_js_1.JWSInvalid("JWS Payload missing");
		if (typeof jws.signature !== "string") throw new errors_js_1.JWSInvalid("JWS Signature missing or incorrect type");
		if (jws.header !== void 0 && !(0, is_object_js_1.default)(jws.header)) throw new errors_js_1.JWSInvalid("JWS Unprotected Header incorrect type");
		let parsedProt = {};
		if (jws.protected) try {
			const protectedHeader = (0, base64url_js_1.decode)(jws.protected);
			parsedProt = JSON.parse(buffer_utils_js_1.decoder.decode(protectedHeader));
		} catch {
			throw new errors_js_1.JWSInvalid("JWS Protected Header is invalid");
		}
		if (!(0, is_disjoint_js_1.default)(parsedProt, jws.header)) throw new errors_js_1.JWSInvalid("JWS Protected and JWS Unprotected Header Parameter names must be disjoint");
		const joseHeader = {
			...parsedProt,
			...jws.header
		};
		const extensions = (0, validate_crit_js_1.default)(errors_js_1.JWSInvalid, /* @__PURE__ */ new Map([["b64", true]]), options?.crit, parsedProt, joseHeader);
		let b64 = true;
		if (extensions.has("b64")) {
			b64 = parsedProt.b64;
			if (typeof b64 !== "boolean") throw new errors_js_1.JWSInvalid("The \"b64\" (base64url-encode payload) Header Parameter must be a boolean");
		}
		const { alg } = joseHeader;
		if (typeof alg !== "string" || !alg) throw new errors_js_1.JWSInvalid("JWS \"alg\" (Algorithm) Header Parameter missing or invalid");
		const algorithms = options && (0, validate_algorithms_js_1.default)("algorithms", options.algorithms);
		if (algorithms && !algorithms.has(alg)) throw new errors_js_1.JOSEAlgNotAllowed("\"alg\" (Algorithm) Header Parameter value not allowed");
		if (b64) {
			if (typeof jws.payload !== "string") throw new errors_js_1.JWSInvalid("JWS Payload must be a string");
		} else if (typeof jws.payload !== "string" && !(jws.payload instanceof Uint8Array)) throw new errors_js_1.JWSInvalid("JWS Payload must be a string or an Uint8Array instance");
		let resolvedKey = false;
		if (typeof key === "function") {
			key = await key(parsedProt, jws);
			resolvedKey = true;
			(0, check_key_type_js_1.checkKeyTypeWithJwk)(alg, key, "verify");
			if ((0, is_jwk_js_1.isJWK)(key)) key = await (0, import_js_1.importJWK)(key, alg);
		} else (0, check_key_type_js_1.checkKeyTypeWithJwk)(alg, key, "verify");
		const data = (0, buffer_utils_js_1.concat)(buffer_utils_js_1.encoder.encode(jws.protected ?? ""), buffer_utils_js_1.encoder.encode("."), typeof jws.payload === "string" ? buffer_utils_js_1.encoder.encode(jws.payload) : jws.payload);
		let signature;
		try {
			signature = (0, base64url_js_1.decode)(jws.signature);
		} catch {
			throw new errors_js_1.JWSInvalid("Failed to base64url decode the signature");
		}
		if (!await (0, verify_js_1.default)(alg, key, signature, data)) throw new errors_js_1.JWSSignatureVerificationFailed();
		let payload;
		if (b64) try {
			payload = (0, base64url_js_1.decode)(jws.payload);
		} catch {
			throw new errors_js_1.JWSInvalid("Failed to base64url decode the payload");
		}
		else if (typeof jws.payload === "string") payload = buffer_utils_js_1.encoder.encode(jws.payload);
		else payload = jws.payload;
		const result = { payload };
		if (jws.protected !== void 0) result.protectedHeader = parsedProt;
		if (jws.header !== void 0) result.unprotectedHeader = jws.header;
		if (resolvedKey) return {
			...result,
			key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/compact/verify.js
var require_verify$2 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.compactVerify = compactVerify;
	const verify_js_1 = require_verify$3();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	async function compactVerify(jws, key, options) {
		if (jws instanceof Uint8Array) jws = buffer_utils_js_1.decoder.decode(jws);
		if (typeof jws !== "string") throw new errors_js_1.JWSInvalid("Compact JWS must be a string or Uint8Array");
		const { 0: protectedHeader, 1: payload, 2: signature, length } = jws.split(".");
		if (length !== 3) throw new errors_js_1.JWSInvalid("Invalid Compact JWS");
		const verified = await (0, verify_js_1.flattenedVerify)({
			payload,
			protected: protectedHeader,
			signature
		}, key, options);
		const result = {
			payload: verified.payload,
			protectedHeader: verified.protectedHeader
		};
		if (typeof key === "function") return {
			...result,
			key: verified.key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/general/verify.js
var require_verify$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.generalVerify = generalVerify;
	const verify_js_1 = require_verify$3();
	const errors_js_1 = require_errors();
	const is_object_js_1 = require_is_object();
	async function generalVerify(jws, key, options) {
		if (!(0, is_object_js_1.default)(jws)) throw new errors_js_1.JWSInvalid("General JWS must be an object");
		if (!Array.isArray(jws.signatures) || !jws.signatures.every(is_object_js_1.default)) throw new errors_js_1.JWSInvalid("JWS Signatures missing or incorrect type");
		for (const signature of jws.signatures) try {
			return await (0, verify_js_1.flattenedVerify)({
				header: signature.header,
				payload: jws.payload,
				protected: signature.protected,
				signature: signature.signature
			}, key, options);
		} catch {}
		throw new errors_js_1.JWSSignatureVerificationFailed();
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/epoch.js
var require_epoch = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = (date) => Math.floor(date.getTime() / 1e3);
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/secs.js
var require_secs = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const minute = 60;
	const hour = 3600;
	const day = hour * 24;
	const week = day * 7;
	const year = day * 365.25;
	const REGEX = /^(\+|\-)? ?(\d+|\d+\.\d+) ?(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d|weeks?|w|years?|yrs?|y)(?: (ago|from now))?$/i;
	exports.default = (str) => {
		const matched = REGEX.exec(str);
		if (!matched || matched[4] && matched[1]) throw new TypeError("Invalid time period format");
		const value = parseFloat(matched[2]);
		const unit = matched[3].toLowerCase();
		let numericDate;
		switch (unit) {
			case "sec":
			case "secs":
			case "second":
			case "seconds":
			case "s":
				numericDate = Math.round(value);
				break;
			case "minute":
			case "minutes":
			case "min":
			case "mins":
			case "m":
				numericDate = Math.round(value * minute);
				break;
			case "hour":
			case "hours":
			case "hr":
			case "hrs":
			case "h":
				numericDate = Math.round(value * hour);
				break;
			case "day":
			case "days":
			case "d":
				numericDate = Math.round(value * day);
				break;
			case "week":
			case "weeks":
			case "w":
				numericDate = Math.round(value * week);
				break;
			default: numericDate = Math.round(value * year);
		}
		if (matched[1] === "-" || matched[4] === "ago") return -numericDate;
		return numericDate;
	};
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/lib/jwt_claims_set.js
var require_jwt_claims_set = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const epoch_js_1 = require_epoch();
	const secs_js_1 = require_secs();
	const is_object_js_1 = require_is_object();
	const normalizeTyp = (value) => value.toLowerCase().replace(/^application\//, "");
	const checkAudiencePresence = (audPayload, audOption) => {
		if (typeof audPayload === "string") return audOption.includes(audPayload);
		if (Array.isArray(audPayload)) return audOption.some(Set.prototype.has.bind(new Set(audPayload)));
		return false;
	};
	exports.default = (protectedHeader, encodedPayload, options = {}) => {
		let payload;
		try {
			payload = JSON.parse(buffer_utils_js_1.decoder.decode(encodedPayload));
		} catch {}
		if (!(0, is_object_js_1.default)(payload)) throw new errors_js_1.JWTInvalid("JWT Claims Set must be a top-level JSON object");
		const { typ } = options;
		if (typ && (typeof protectedHeader.typ !== "string" || normalizeTyp(protectedHeader.typ) !== normalizeTyp(typ))) throw new errors_js_1.JWTClaimValidationFailed("unexpected \"typ\" JWT header value", payload, "typ", "check_failed");
		const { requiredClaims = [], issuer, subject, audience, maxTokenAge } = options;
		const presenceCheck = [...requiredClaims];
		if (maxTokenAge !== void 0) presenceCheck.push("iat");
		if (audience !== void 0) presenceCheck.push("aud");
		if (subject !== void 0) presenceCheck.push("sub");
		if (issuer !== void 0) presenceCheck.push("iss");
		for (const claim of new Set(presenceCheck.reverse())) if (!(claim in payload)) throw new errors_js_1.JWTClaimValidationFailed(`missing required "${claim}" claim`, payload, claim, "missing");
		if (issuer && !(Array.isArray(issuer) ? issuer : [issuer]).includes(payload.iss)) throw new errors_js_1.JWTClaimValidationFailed("unexpected \"iss\" claim value", payload, "iss", "check_failed");
		if (subject && payload.sub !== subject) throw new errors_js_1.JWTClaimValidationFailed("unexpected \"sub\" claim value", payload, "sub", "check_failed");
		if (audience && !checkAudiencePresence(payload.aud, typeof audience === "string" ? [audience] : audience)) throw new errors_js_1.JWTClaimValidationFailed("unexpected \"aud\" claim value", payload, "aud", "check_failed");
		let tolerance;
		switch (typeof options.clockTolerance) {
			case "string":
				tolerance = (0, secs_js_1.default)(options.clockTolerance);
				break;
			case "number":
				tolerance = options.clockTolerance;
				break;
			case "undefined":
				tolerance = 0;
				break;
			default: throw new TypeError("Invalid clockTolerance option type");
		}
		const { currentDate } = options;
		const now = (0, epoch_js_1.default)(currentDate || /* @__PURE__ */ new Date());
		if ((payload.iat !== void 0 || maxTokenAge) && typeof payload.iat !== "number") throw new errors_js_1.JWTClaimValidationFailed("\"iat\" claim must be a number", payload, "iat", "invalid");
		if (payload.nbf !== void 0) {
			if (typeof payload.nbf !== "number") throw new errors_js_1.JWTClaimValidationFailed("\"nbf\" claim must be a number", payload, "nbf", "invalid");
			if (payload.nbf > now + tolerance) throw new errors_js_1.JWTClaimValidationFailed("\"nbf\" claim timestamp check failed", payload, "nbf", "check_failed");
		}
		if (payload.exp !== void 0) {
			if (typeof payload.exp !== "number") throw new errors_js_1.JWTClaimValidationFailed("\"exp\" claim must be a number", payload, "exp", "invalid");
			if (payload.exp <= now - tolerance) throw new errors_js_1.JWTExpired("\"exp\" claim timestamp check failed", payload, "exp", "check_failed");
		}
		if (maxTokenAge) {
			const age = now - payload.iat;
			const max = typeof maxTokenAge === "number" ? maxTokenAge : (0, secs_js_1.default)(maxTokenAge);
			if (age - tolerance > max) throw new errors_js_1.JWTExpired("\"iat\" claim timestamp check failed (too far in the past)", payload, "iat", "check_failed");
			if (age < 0 - tolerance) throw new errors_js_1.JWTClaimValidationFailed("\"iat\" claim timestamp check failed (it should be in the past)", payload, "iat", "check_failed");
		}
		return payload;
	};
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/verify.js
var require_verify = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.jwtVerify = jwtVerify;
	const verify_js_1 = require_verify$2();
	const jwt_claims_set_js_1 = require_jwt_claims_set();
	const errors_js_1 = require_errors();
	async function jwtVerify(jwt, key, options) {
		const verified = await (0, verify_js_1.compactVerify)(jwt, key, options);
		if (verified.protectedHeader.crit?.includes("b64") && verified.protectedHeader.b64 === false) throw new errors_js_1.JWTInvalid("JWTs MUST NOT use unencoded payload");
		const result = {
			payload: (0, jwt_claims_set_js_1.default)(verified.protectedHeader, verified.payload, options),
			protectedHeader: verified.protectedHeader
		};
		if (typeof key === "function") return {
			...result,
			key: verified.key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/decrypt.js
var require_decrypt = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.jwtDecrypt = jwtDecrypt;
	const decrypt_js_1 = require_decrypt$2();
	const jwt_claims_set_js_1 = require_jwt_claims_set();
	const errors_js_1 = require_errors();
	async function jwtDecrypt(jwt, key, options) {
		const decrypted = await (0, decrypt_js_1.compactDecrypt)(jwt, key, options);
		const payload = (0, jwt_claims_set_js_1.default)(decrypted.protectedHeader, decrypted.plaintext, options);
		const { protectedHeader } = decrypted;
		if (protectedHeader.iss !== void 0 && protectedHeader.iss !== payload.iss) throw new errors_js_1.JWTClaimValidationFailed("replicated \"iss\" claim header parameter mismatch", payload, "iss", "mismatch");
		if (protectedHeader.sub !== void 0 && protectedHeader.sub !== payload.sub) throw new errors_js_1.JWTClaimValidationFailed("replicated \"sub\" claim header parameter mismatch", payload, "sub", "mismatch");
		if (protectedHeader.aud !== void 0 && JSON.stringify(protectedHeader.aud) !== JSON.stringify(payload.aud)) throw new errors_js_1.JWTClaimValidationFailed("replicated \"aud\" claim header parameter mismatch", payload, "aud", "mismatch");
		const result = {
			payload,
			protectedHeader
		};
		if (typeof key === "function") return {
			...result,
			key: decrypted.key
		};
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwe/compact/encrypt.js
var require_encrypt$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.CompactEncrypt = void 0;
	const encrypt_js_1 = require_encrypt$3();
	var CompactEncrypt = class {
		_flattened;
		constructor(plaintext) {
			this._flattened = new encrypt_js_1.FlattenedEncrypt(plaintext);
		}
		setContentEncryptionKey(cek) {
			this._flattened.setContentEncryptionKey(cek);
			return this;
		}
		setInitializationVector(iv) {
			this._flattened.setInitializationVector(iv);
			return this;
		}
		setProtectedHeader(protectedHeader) {
			this._flattened.setProtectedHeader(protectedHeader);
			return this;
		}
		setKeyManagementParameters(parameters) {
			this._flattened.setKeyManagementParameters(parameters);
			return this;
		}
		async encrypt(key, options) {
			const jwe = await this._flattened.encrypt(key, options);
			return [
				jwe.protected,
				jwe.encrypted_key,
				jwe.iv,
				jwe.ciphertext,
				jwe.tag
			].join(".");
		}
	};
	exports.CompactEncrypt = CompactEncrypt;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/flattened/sign.js
var require_sign$3 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.FlattenedSign = void 0;
	const base64url_js_1 = require_base64url$1();
	const sign_js_1 = require_sign$4();
	const is_disjoint_js_1 = require_is_disjoint();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const check_key_type_js_1 = require_check_key_type();
	const validate_crit_js_1 = require_validate_crit();
	var FlattenedSign = class {
		_payload;
		_protectedHeader;
		_unprotectedHeader;
		constructor(payload) {
			if (!(payload instanceof Uint8Array)) throw new TypeError("payload must be an instance of Uint8Array");
			this._payload = payload;
		}
		setProtectedHeader(protectedHeader) {
			if (this._protectedHeader) throw new TypeError("setProtectedHeader can only be called once");
			this._protectedHeader = protectedHeader;
			return this;
		}
		setUnprotectedHeader(unprotectedHeader) {
			if (this._unprotectedHeader) throw new TypeError("setUnprotectedHeader can only be called once");
			this._unprotectedHeader = unprotectedHeader;
			return this;
		}
		async sign(key, options) {
			if (!this._protectedHeader && !this._unprotectedHeader) throw new errors_js_1.JWSInvalid("either setProtectedHeader or setUnprotectedHeader must be called before #sign()");
			if (!(0, is_disjoint_js_1.default)(this._protectedHeader, this._unprotectedHeader)) throw new errors_js_1.JWSInvalid("JWS Protected and JWS Unprotected Header Parameter names must be disjoint");
			const joseHeader = {
				...this._protectedHeader,
				...this._unprotectedHeader
			};
			const extensions = (0, validate_crit_js_1.default)(errors_js_1.JWSInvalid, /* @__PURE__ */ new Map([["b64", true]]), options?.crit, this._protectedHeader, joseHeader);
			let b64 = true;
			if (extensions.has("b64")) {
				b64 = this._protectedHeader.b64;
				if (typeof b64 !== "boolean") throw new errors_js_1.JWSInvalid("The \"b64\" (base64url-encode payload) Header Parameter must be a boolean");
			}
			const { alg } = joseHeader;
			if (typeof alg !== "string" || !alg) throw new errors_js_1.JWSInvalid("JWS \"alg\" (Algorithm) Header Parameter missing or invalid");
			(0, check_key_type_js_1.checkKeyTypeWithJwk)(alg, key, "sign");
			let payload = this._payload;
			if (b64) payload = buffer_utils_js_1.encoder.encode((0, base64url_js_1.encode)(payload));
			let protectedHeader;
			if (this._protectedHeader) protectedHeader = buffer_utils_js_1.encoder.encode((0, base64url_js_1.encode)(JSON.stringify(this._protectedHeader)));
			else protectedHeader = buffer_utils_js_1.encoder.encode("");
			const data = (0, buffer_utils_js_1.concat)(protectedHeader, buffer_utils_js_1.encoder.encode("."), payload);
			const signature = await (0, sign_js_1.default)(alg, key, data);
			const jws = {
				signature: (0, base64url_js_1.encode)(signature),
				payload: ""
			};
			if (b64) jws.payload = buffer_utils_js_1.decoder.decode(payload);
			if (this._unprotectedHeader) jws.header = this._unprotectedHeader;
			if (this._protectedHeader) jws.protected = buffer_utils_js_1.decoder.decode(protectedHeader);
			return jws;
		}
	};
	exports.FlattenedSign = FlattenedSign;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/compact/sign.js
var require_sign$2 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.CompactSign = void 0;
	const sign_js_1 = require_sign$3();
	var CompactSign = class {
		_flattened;
		constructor(payload) {
			this._flattened = new sign_js_1.FlattenedSign(payload);
		}
		setProtectedHeader(protectedHeader) {
			this._flattened.setProtectedHeader(protectedHeader);
			return this;
		}
		async sign(key, options) {
			const jws = await this._flattened.sign(key, options);
			if (jws.payload === void 0) throw new TypeError("use the flattened module for creating JWS with b64: false");
			return `${jws.protected}.${jws.payload}.${jws.signature}`;
		}
	};
	exports.CompactSign = CompactSign;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jws/general/sign.js
var require_sign$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.GeneralSign = void 0;
	const sign_js_1 = require_sign$3();
	const errors_js_1 = require_errors();
	var IndividualSignature = class {
		parent;
		protectedHeader;
		unprotectedHeader;
		options;
		key;
		constructor(sig, key, options) {
			this.parent = sig;
			this.key = key;
			this.options = options;
		}
		setProtectedHeader(protectedHeader) {
			if (this.protectedHeader) throw new TypeError("setProtectedHeader can only be called once");
			this.protectedHeader = protectedHeader;
			return this;
		}
		setUnprotectedHeader(unprotectedHeader) {
			if (this.unprotectedHeader) throw new TypeError("setUnprotectedHeader can only be called once");
			this.unprotectedHeader = unprotectedHeader;
			return this;
		}
		addSignature(...args) {
			return this.parent.addSignature(...args);
		}
		sign(...args) {
			return this.parent.sign(...args);
		}
		done() {
			return this.parent;
		}
	};
	var GeneralSign = class {
		_payload;
		_signatures = [];
		constructor(payload) {
			this._payload = payload;
		}
		addSignature(key, options) {
			const signature = new IndividualSignature(this, key, options);
			this._signatures.push(signature);
			return signature;
		}
		async sign() {
			if (!this._signatures.length) throw new errors_js_1.JWSInvalid("at least one signature must be added");
			const jws = {
				signatures: [],
				payload: ""
			};
			for (let i = 0; i < this._signatures.length; i++) {
				const signature = this._signatures[i];
				const flattened = new sign_js_1.FlattenedSign(this._payload);
				flattened.setProtectedHeader(signature.protectedHeader);
				flattened.setUnprotectedHeader(signature.unprotectedHeader);
				const { payload, ...rest } = await flattened.sign(signature.key, signature.options);
				if (i === 0) jws.payload = payload;
				else if (jws.payload !== payload) throw new errors_js_1.JWSInvalid("inconsistent use of JWS Unencoded Payload (RFC7797)");
				jws.signatures.push(rest);
			}
			return jws;
		}
	};
	exports.GeneralSign = GeneralSign;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/produce.js
var require_produce = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.ProduceJWT = void 0;
	const epoch_js_1 = require_epoch();
	const is_object_js_1 = require_is_object();
	const secs_js_1 = require_secs();
	function validateInput(label, input) {
		if (!Number.isFinite(input)) throw new TypeError(`Invalid ${label} input`);
		return input;
	}
	var ProduceJWT = class {
		_payload;
		constructor(payload = {}) {
			if (!(0, is_object_js_1.default)(payload)) throw new TypeError("JWT Claims Set MUST be an object");
			this._payload = payload;
		}
		setIssuer(issuer) {
			this._payload = {
				...this._payload,
				iss: issuer
			};
			return this;
		}
		setSubject(subject) {
			this._payload = {
				...this._payload,
				sub: subject
			};
			return this;
		}
		setAudience(audience) {
			this._payload = {
				...this._payload,
				aud: audience
			};
			return this;
		}
		setJti(jwtId) {
			this._payload = {
				...this._payload,
				jti: jwtId
			};
			return this;
		}
		setNotBefore(input) {
			if (typeof input === "number") this._payload = {
				...this._payload,
				nbf: validateInput("setNotBefore", input)
			};
			else if (input instanceof Date) this._payload = {
				...this._payload,
				nbf: validateInput("setNotBefore", (0, epoch_js_1.default)(input))
			};
			else this._payload = {
				...this._payload,
				nbf: (0, epoch_js_1.default)(/* @__PURE__ */ new Date()) + (0, secs_js_1.default)(input)
			};
			return this;
		}
		setExpirationTime(input) {
			if (typeof input === "number") this._payload = {
				...this._payload,
				exp: validateInput("setExpirationTime", input)
			};
			else if (input instanceof Date) this._payload = {
				...this._payload,
				exp: validateInput("setExpirationTime", (0, epoch_js_1.default)(input))
			};
			else this._payload = {
				...this._payload,
				exp: (0, epoch_js_1.default)(/* @__PURE__ */ new Date()) + (0, secs_js_1.default)(input)
			};
			return this;
		}
		setIssuedAt(input) {
			if (typeof input === "undefined") this._payload = {
				...this._payload,
				iat: (0, epoch_js_1.default)(/* @__PURE__ */ new Date())
			};
			else if (input instanceof Date) this._payload = {
				...this._payload,
				iat: validateInput("setIssuedAt", (0, epoch_js_1.default)(input))
			};
			else if (typeof input === "string") this._payload = {
				...this._payload,
				iat: validateInput("setIssuedAt", (0, epoch_js_1.default)(/* @__PURE__ */ new Date()) + (0, secs_js_1.default)(input))
			};
			else this._payload = {
				...this._payload,
				iat: validateInput("setIssuedAt", input)
			};
			return this;
		}
	};
	exports.ProduceJWT = ProduceJWT;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/sign.js
var require_sign = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.SignJWT = void 0;
	const sign_js_1 = require_sign$2();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const produce_js_1 = require_produce();
	var SignJWT = class extends produce_js_1.ProduceJWT {
		_protectedHeader;
		setProtectedHeader(protectedHeader) {
			this._protectedHeader = protectedHeader;
			return this;
		}
		async sign(key, options) {
			const sig = new sign_js_1.CompactSign(buffer_utils_js_1.encoder.encode(JSON.stringify(this._payload)));
			sig.setProtectedHeader(this._protectedHeader);
			if (Array.isArray(this._protectedHeader?.crit) && this._protectedHeader.crit.includes("b64") && this._protectedHeader.b64 === false) throw new errors_js_1.JWTInvalid("JWTs MUST NOT use unencoded payload");
			return sig.sign(key, options);
		}
	};
	exports.SignJWT = SignJWT;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/encrypt.js
var require_encrypt = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.EncryptJWT = void 0;
	const encrypt_js_1 = require_encrypt$1();
	const buffer_utils_js_1 = require_buffer_utils();
	const produce_js_1 = require_produce();
	var EncryptJWT = class extends produce_js_1.ProduceJWT {
		_cek;
		_iv;
		_keyManagementParameters;
		_protectedHeader;
		_replicateIssuerAsHeader;
		_replicateSubjectAsHeader;
		_replicateAudienceAsHeader;
		setProtectedHeader(protectedHeader) {
			if (this._protectedHeader) throw new TypeError("setProtectedHeader can only be called once");
			this._protectedHeader = protectedHeader;
			return this;
		}
		setKeyManagementParameters(parameters) {
			if (this._keyManagementParameters) throw new TypeError("setKeyManagementParameters can only be called once");
			this._keyManagementParameters = parameters;
			return this;
		}
		setContentEncryptionKey(cek) {
			if (this._cek) throw new TypeError("setContentEncryptionKey can only be called once");
			this._cek = cek;
			return this;
		}
		setInitializationVector(iv) {
			if (this._iv) throw new TypeError("setInitializationVector can only be called once");
			this._iv = iv;
			return this;
		}
		replicateIssuerAsHeader() {
			this._replicateIssuerAsHeader = true;
			return this;
		}
		replicateSubjectAsHeader() {
			this._replicateSubjectAsHeader = true;
			return this;
		}
		replicateAudienceAsHeader() {
			this._replicateAudienceAsHeader = true;
			return this;
		}
		async encrypt(key, options) {
			const enc = new encrypt_js_1.CompactEncrypt(buffer_utils_js_1.encoder.encode(JSON.stringify(this._payload)));
			if (this._replicateIssuerAsHeader) this._protectedHeader = {
				...this._protectedHeader,
				iss: this._payload.iss
			};
			if (this._replicateSubjectAsHeader) this._protectedHeader = {
				...this._protectedHeader,
				sub: this._payload.sub
			};
			if (this._replicateAudienceAsHeader) this._protectedHeader = {
				...this._protectedHeader,
				aud: this._payload.aud
			};
			enc.setProtectedHeader(this._protectedHeader);
			if (this._iv) enc.setInitializationVector(this._iv);
			if (this._cek) enc.setContentEncryptionKey(this._cek);
			if (this._keyManagementParameters) enc.setKeyManagementParameters(this._keyManagementParameters);
			return enc.encrypt(key, options);
		}
	};
	exports.EncryptJWT = EncryptJWT;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwk/thumbprint.js
var require_thumbprint = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.calculateJwkThumbprint = calculateJwkThumbprint;
	exports.calculateJwkThumbprintUri = calculateJwkThumbprintUri;
	const digest_js_1 = require_digest();
	const base64url_js_1 = require_base64url$1();
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const is_object_js_1 = require_is_object();
	const check = (value, description) => {
		if (typeof value !== "string" || !value) throw new errors_js_1.JWKInvalid(`${description} missing or invalid`);
	};
	async function calculateJwkThumbprint(jwk, digestAlgorithm) {
		if (!(0, is_object_js_1.default)(jwk)) throw new TypeError("JWK must be an object");
		digestAlgorithm ??= "sha256";
		if (digestAlgorithm !== "sha256" && digestAlgorithm !== "sha384" && digestAlgorithm !== "sha512") throw new TypeError("digestAlgorithm must one of \"sha256\", \"sha384\", or \"sha512\"");
		let components;
		switch (jwk.kty) {
			case "EC":
				check(jwk.crv, "\"crv\" (Curve) Parameter");
				check(jwk.x, "\"x\" (X Coordinate) Parameter");
				check(jwk.y, "\"y\" (Y Coordinate) Parameter");
				components = {
					crv: jwk.crv,
					kty: jwk.kty,
					x: jwk.x,
					y: jwk.y
				};
				break;
			case "OKP":
				check(jwk.crv, "\"crv\" (Subtype of Key Pair) Parameter");
				check(jwk.x, "\"x\" (Public Key) Parameter");
				components = {
					crv: jwk.crv,
					kty: jwk.kty,
					x: jwk.x
				};
				break;
			case "RSA":
				check(jwk.e, "\"e\" (Exponent) Parameter");
				check(jwk.n, "\"n\" (Modulus) Parameter");
				components = {
					e: jwk.e,
					kty: jwk.kty,
					n: jwk.n
				};
				break;
			case "oct":
				check(jwk.k, "\"k\" (Key Value) Parameter");
				components = {
					k: jwk.k,
					kty: jwk.kty
				};
				break;
			default: throw new errors_js_1.JOSENotSupported("\"kty\" (Key Type) Parameter missing or unsupported");
		}
		const data = buffer_utils_js_1.encoder.encode(JSON.stringify(components));
		return (0, base64url_js_1.encode)(await (0, digest_js_1.default)(digestAlgorithm, data));
	}
	async function calculateJwkThumbprintUri(jwk, digestAlgorithm) {
		digestAlgorithm ??= "sha256";
		const thumbprint = await calculateJwkThumbprint(jwk, digestAlgorithm);
		return `urn:ietf:params:oauth:jwk-thumbprint:sha-${digestAlgorithm.slice(-3)}:${thumbprint}`;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwk/embedded.js
var require_embedded = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.EmbeddedJWK = EmbeddedJWK;
	const import_js_1 = require_import();
	const is_object_js_1 = require_is_object();
	const errors_js_1 = require_errors();
	async function EmbeddedJWK(protectedHeader, token) {
		const joseHeader = {
			...protectedHeader,
			...token?.header
		};
		if (!(0, is_object_js_1.default)(joseHeader.jwk)) throw new errors_js_1.JWSInvalid("\"jwk\" (JSON Web Key) Header Parameter must be a JSON object");
		const key = await (0, import_js_1.importJWK)({
			...joseHeader.jwk,
			ext: true
		}, joseHeader.alg);
		if (key instanceof Uint8Array || key.type !== "public") throw new errors_js_1.JWSInvalid("\"jwk\" (JSON Web Key) Header Parameter must be a public key");
		return key;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwks/local.js
var require_local = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.createLocalJWKSet = createLocalJWKSet;
	const import_js_1 = require_import();
	const errors_js_1 = require_errors();
	const is_object_js_1 = require_is_object();
	function getKtyFromAlg(alg) {
		switch (typeof alg === "string" && alg.slice(0, 2)) {
			case "RS":
			case "PS": return "RSA";
			case "ES": return "EC";
			case "Ed": return "OKP";
			default: throw new errors_js_1.JOSENotSupported("Unsupported \"alg\" value for a JSON Web Key Set");
		}
	}
	function isJWKSLike(jwks) {
		return jwks && typeof jwks === "object" && Array.isArray(jwks.keys) && jwks.keys.every(isJWKLike);
	}
	function isJWKLike(key) {
		return (0, is_object_js_1.default)(key);
	}
	function clone(obj) {
		if (typeof structuredClone === "function") return structuredClone(obj);
		return JSON.parse(JSON.stringify(obj));
	}
	var LocalJWKSet = class {
		_jwks;
		_cached = /* @__PURE__ */ new WeakMap();
		constructor(jwks) {
			if (!isJWKSLike(jwks)) throw new errors_js_1.JWKSInvalid("JSON Web Key Set malformed");
			this._jwks = clone(jwks);
		}
		async getKey(protectedHeader, token) {
			const { alg, kid } = {
				...protectedHeader,
				...token?.header
			};
			const kty = getKtyFromAlg(alg);
			const candidates = this._jwks.keys.filter((jwk) => {
				let candidate = kty === jwk.kty;
				if (candidate && typeof kid === "string") candidate = kid === jwk.kid;
				if (candidate && typeof jwk.alg === "string") candidate = alg === jwk.alg;
				if (candidate && typeof jwk.use === "string") candidate = jwk.use === "sig";
				if (candidate && Array.isArray(jwk.key_ops)) candidate = jwk.key_ops.includes("verify");
				if (candidate) switch (alg) {
					case "ES256":
						candidate = jwk.crv === "P-256";
						break;
					case "ES256K":
						candidate = jwk.crv === "secp256k1";
						break;
					case "ES384":
						candidate = jwk.crv === "P-384";
						break;
					case "ES512":
						candidate = jwk.crv === "P-521";
						break;
					case "Ed25519":
						candidate = jwk.crv === "Ed25519";
						break;
					case "EdDSA": candidate = jwk.crv === "Ed25519" || jwk.crv === "Ed448";
				}
				return candidate;
			});
			const { 0: jwk, length } = candidates;
			if (length === 0) throw new errors_js_1.JWKSNoMatchingKey();
			if (length !== 1) {
				const error = new errors_js_1.JWKSMultipleMatchingKeys();
				const { _cached } = this;
				error[Symbol.asyncIterator] = async function* () {
					for (const jwk of candidates) try {
						yield await importWithAlgCache(_cached, jwk, alg);
					} catch {}
				};
				throw error;
			}
			return importWithAlgCache(this._cached, jwk, alg);
		}
	};
	async function importWithAlgCache(cache, jwk, alg) {
		const cached = cache.get(jwk) || cache.set(jwk, {}).get(jwk);
		if (cached[alg] === void 0) {
			const key = await (0, import_js_1.importJWK)({
				...jwk,
				ext: true
			}, alg);
			if (key instanceof Uint8Array || key.type !== "public") throw new errors_js_1.JWKSInvalid("JSON Web Key Set members must be public keys");
			cached[alg] = key;
		}
		return cached[alg];
	}
	function createLocalJWKSet(jwks) {
		const set = new LocalJWKSet(jwks);
		const localJWKSet = async (protectedHeader, token) => set.getKey(protectedHeader, token);
		Object.defineProperties(localJWKSet, { jwks: {
			value: () => clone(set._jwks),
			enumerable: true,
			configurable: false,
			writable: false
		} });
		return localJWKSet;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/fetch_jwks.js
var require_fetch_jwks = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	const http = __require("node:http");
	const https = __require("node:https");
	const node_events_1 = __require("node:events");
	const errors_js_1 = require_errors();
	const buffer_utils_js_1 = require_buffer_utils();
	const fetchJwks = async (url, timeout, options) => {
		let get;
		switch (url.protocol) {
			case "https:":
				get = https.get;
				break;
			case "http:":
				get = http.get;
				break;
			default: throw new TypeError("Unsupported URL protocol.");
		}
		const { agent, headers } = options;
		const req = get(url.href, {
			agent,
			timeout,
			headers
		});
		const [response] = await Promise.race([(0, node_events_1.once)(req, "response"), (0, node_events_1.once)(req, "timeout")]);
		if (!response) {
			req.destroy();
			throw new errors_js_1.JWKSTimeout();
		}
		if (response.statusCode !== 200) throw new errors_js_1.JOSEError("Expected 200 OK from the JSON Web Key Set HTTP response");
		const parts = [];
		for await (const part of response) parts.push(part);
		try {
			return JSON.parse(buffer_utils_js_1.decoder.decode((0, buffer_utils_js_1.concat)(...parts)));
		} catch {
			throw new errors_js_1.JOSEError("Failed to parse the JSON Web Key Set HTTP response as JSON");
		}
	};
	exports.default = fetchJwks;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwks/remote.js
var require_remote = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.experimental_jwksCache = exports.jwksCache = void 0;
	exports.createRemoteJWKSet = createRemoteJWKSet;
	const fetch_jwks_js_1 = require_fetch_jwks();
	const errors_js_1 = require_errors();
	const local_js_1 = require_local();
	const is_object_js_1 = require_is_object();
	function isCloudflareWorkers() {
		return typeof WebSocketPair !== "undefined" || typeof navigator !== "undefined" && navigator.userAgent === "Cloudflare-Workers" || typeof EdgeRuntime !== "undefined" && EdgeRuntime === "vercel";
	}
	let USER_AGENT;
	if (typeof navigator === "undefined" || !navigator.userAgent?.startsWith?.("Mozilla/5.0 ")) USER_AGENT = `jose/v5.10.0`;
	exports.jwksCache = Symbol();
	function isFreshJwksCache(input, cacheMaxAge) {
		if (typeof input !== "object" || input === null) return false;
		if (!("uat" in input) || typeof input.uat !== "number" || Date.now() - input.uat >= cacheMaxAge) return false;
		if (!("jwks" in input) || !(0, is_object_js_1.default)(input.jwks) || !Array.isArray(input.jwks.keys) || !Array.prototype.every.call(input.jwks.keys, is_object_js_1.default)) return false;
		return true;
	}
	var RemoteJWKSet = class {
		_url;
		_timeoutDuration;
		_cooldownDuration;
		_cacheMaxAge;
		_jwksTimestamp;
		_pendingFetch;
		_options;
		_local;
		_cache;
		constructor(url, options) {
			if (!(url instanceof URL)) throw new TypeError("url must be an instance of URL");
			this._url = new URL(url.href);
			this._options = {
				agent: options?.agent,
				headers: options?.headers
			};
			this._timeoutDuration = typeof options?.timeoutDuration === "number" ? options?.timeoutDuration : 5e3;
			this._cooldownDuration = typeof options?.cooldownDuration === "number" ? options?.cooldownDuration : 3e4;
			this._cacheMaxAge = typeof options?.cacheMaxAge === "number" ? options?.cacheMaxAge : 6e5;
			if (options?.[exports.jwksCache] !== void 0) {
				this._cache = options?.[exports.jwksCache];
				if (isFreshJwksCache(options?.[exports.jwksCache], this._cacheMaxAge)) {
					this._jwksTimestamp = this._cache.uat;
					this._local = (0, local_js_1.createLocalJWKSet)(this._cache.jwks);
				}
			}
		}
		coolingDown() {
			return typeof this._jwksTimestamp === "number" ? Date.now() < this._jwksTimestamp + this._cooldownDuration : false;
		}
		fresh() {
			return typeof this._jwksTimestamp === "number" ? Date.now() < this._jwksTimestamp + this._cacheMaxAge : false;
		}
		async getKey(protectedHeader, token) {
			if (!this._local || !this.fresh()) await this.reload();
			try {
				return await this._local(protectedHeader, token);
			} catch (err) {
				if (err instanceof errors_js_1.JWKSNoMatchingKey) {
					if (this.coolingDown() === false) {
						await this.reload();
						return this._local(protectedHeader, token);
					}
				}
				throw err;
			}
		}
		async reload() {
			if (this._pendingFetch && isCloudflareWorkers()) this._pendingFetch = void 0;
			const headers = new Headers(this._options.headers);
			if (USER_AGENT && !headers.has("User-Agent")) {
				headers.set("User-Agent", USER_AGENT);
				this._options.headers = Object.fromEntries(headers.entries());
			}
			this._pendingFetch ||= (0, fetch_jwks_js_1.default)(this._url, this._timeoutDuration, this._options).then((json) => {
				this._local = (0, local_js_1.createLocalJWKSet)(json);
				if (this._cache) {
					this._cache.uat = Date.now();
					this._cache.jwks = json;
				}
				this._jwksTimestamp = Date.now();
				this._pendingFetch = void 0;
			}).catch((err) => {
				this._pendingFetch = void 0;
				throw err;
			});
			await this._pendingFetch;
		}
	};
	function createRemoteJWKSet(url, options) {
		const set = new RemoteJWKSet(url, options);
		const remoteJWKSet = async (protectedHeader, token) => set.getKey(protectedHeader, token);
		Object.defineProperties(remoteJWKSet, {
			coolingDown: {
				get: () => set.coolingDown(),
				enumerable: true,
				configurable: false
			},
			fresh: {
				get: () => set.fresh(),
				enumerable: true,
				configurable: false
			},
			reload: {
				value: () => set.reload(),
				enumerable: true,
				configurable: false,
				writable: false
			},
			reloading: {
				get: () => !!set._pendingFetch,
				enumerable: true,
				configurable: false
			},
			jwks: {
				value: () => set._local?.jwks(),
				enumerable: true,
				configurable: false,
				writable: false
			}
		});
		return remoteJWKSet;
	}
	exports.experimental_jwksCache = exports.jwksCache;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/jwt/unsecured.js
var require_unsecured = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.UnsecuredJWT = void 0;
	const base64url = require_base64url$1();
	const buffer_utils_js_1 = require_buffer_utils();
	const errors_js_1 = require_errors();
	const jwt_claims_set_js_1 = require_jwt_claims_set();
	const produce_js_1 = require_produce();
	var UnsecuredJWT = class extends produce_js_1.ProduceJWT {
		encode() {
			return `${base64url.encode(JSON.stringify({ alg: "none" }))}.${base64url.encode(JSON.stringify(this._payload))}.`;
		}
		static decode(jwt, options) {
			if (typeof jwt !== "string") throw new errors_js_1.JWTInvalid("Unsecured JWT must be a string");
			const { 0: encodedHeader, 1: encodedPayload, 2: signature, length } = jwt.split(".");
			if (length !== 3 || signature !== "") throw new errors_js_1.JWTInvalid("Invalid Unsecured JWT");
			let header;
			try {
				header = JSON.parse(buffer_utils_js_1.decoder.decode(base64url.decode(encodedHeader)));
				if (header.alg !== "none") throw new Error();
			} catch {
				throw new errors_js_1.JWTInvalid("Invalid Unsecured JWT");
			}
			return {
				payload: (0, jwt_claims_set_js_1.default)(header, base64url.decode(encodedPayload), options),
				header
			};
		}
	};
	exports.UnsecuredJWT = UnsecuredJWT;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/util/base64url.js
var require_base64url = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decode = exports.encode = void 0;
	const base64url = require_base64url$1();
	exports.encode = base64url.encode;
	exports.decode = base64url.decode;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/util/decode_protected_header.js
var require_decode_protected_header = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decodeProtectedHeader = decodeProtectedHeader;
	const base64url_js_1 = require_base64url();
	const buffer_utils_js_1 = require_buffer_utils();
	const is_object_js_1 = require_is_object();
	function decodeProtectedHeader(token) {
		let protectedB64u;
		if (typeof token === "string") {
			const parts = token.split(".");
			if (parts.length === 3 || parts.length === 5) [protectedB64u] = parts;
		} else if (typeof token === "object" && token) {
			if ("protected" in token) protectedB64u = token.protected;
			else throw new TypeError("Token does not contain a Protected Header");
		}
		try {
			if (typeof protectedB64u !== "string" || !protectedB64u) throw new Error();
			const result = JSON.parse(buffer_utils_js_1.decoder.decode((0, base64url_js_1.decode)(protectedB64u)));
			if (!(0, is_object_js_1.default)(result)) throw new Error();
			return result;
		} catch {
			throw new TypeError("Invalid Token or Protected Header formatting");
		}
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/util/decode_jwt.js
var require_decode_jwt = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.decodeJwt = decodeJwt;
	const base64url_js_1 = require_base64url();
	const buffer_utils_js_1 = require_buffer_utils();
	const is_object_js_1 = require_is_object();
	const errors_js_1 = require_errors();
	function decodeJwt(jwt) {
		if (typeof jwt !== "string") throw new errors_js_1.JWTInvalid("JWTs must use Compact JWS serialization, JWT must be a string");
		const { 1: payload, length } = jwt.split(".");
		if (length === 5) throw new errors_js_1.JWTInvalid("Only JWTs using Compact JWS serialization can be decoded");
		if (length !== 3) throw new errors_js_1.JWTInvalid("Invalid JWT");
		if (!payload) throw new errors_js_1.JWTInvalid("JWTs must contain a payload");
		let decoded;
		try {
			decoded = (0, base64url_js_1.decode)(payload);
		} catch {
			throw new errors_js_1.JWTInvalid("Failed to base64url decode the payload");
		}
		let result;
		try {
			result = JSON.parse(buffer_utils_js_1.decoder.decode(decoded));
		} catch {
			throw new errors_js_1.JWTInvalid("Failed to parse the decoded payload as JSON");
		}
		if (!(0, is_object_js_1.default)(result)) throw new errors_js_1.JWTInvalid("Invalid JWT Claims Set");
		return result;
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/generate.js
var require_generate = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.generateSecret = generateSecret;
	exports.generateKeyPair = generateKeyPair;
	const node_crypto_1 = __require("node:crypto");
	const node_util_1 = __require("node:util");
	const random_js_1 = require_random();
	const errors_js_1 = require_errors();
	const generate = (0, node_util_1.promisify)(node_crypto_1.generateKeyPair);
	async function generateSecret(alg, options) {
		let length;
		switch (alg) {
			case "HS256":
			case "HS384":
			case "HS512":
			case "A128CBC-HS256":
			case "A192CBC-HS384":
			case "A256CBC-HS512":
				length = parseInt(alg.slice(-3), 10);
				break;
			case "A128KW":
			case "A192KW":
			case "A256KW":
			case "A128GCMKW":
			case "A192GCMKW":
			case "A256GCMKW":
			case "A128GCM":
			case "A192GCM":
			case "A256GCM":
				length = parseInt(alg.slice(1, 4), 10);
				break;
			default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported JWK \"alg\" (Algorithm) Parameter value");
		}
		return (0, node_crypto_1.createSecretKey)((0, random_js_1.default)(new Uint8Array(length >> 3)));
	}
	async function generateKeyPair(alg, options) {
		switch (alg) {
			case "RS256":
			case "RS384":
			case "RS512":
			case "PS256":
			case "PS384":
			case "PS512":
			case "RSA-OAEP":
			case "RSA-OAEP-256":
			case "RSA-OAEP-384":
			case "RSA-OAEP-512":
			case "RSA1_5": {
				const modulusLength = options?.modulusLength ?? 2048;
				if (typeof modulusLength !== "number" || modulusLength < 2048) throw new errors_js_1.JOSENotSupported("Invalid or unsupported modulusLength option provided, 2048 bits or larger keys must be used");
				return await generate("rsa", {
					modulusLength,
					publicExponent: 65537
				});
			}
			case "ES256": return generate("ec", { namedCurve: "P-256" });
			case "ES256K": return generate("ec", { namedCurve: "secp256k1" });
			case "ES384": return generate("ec", { namedCurve: "P-384" });
			case "ES512": return generate("ec", { namedCurve: "P-521" });
			case "Ed25519": return generate("ed25519");
			case "EdDSA": switch (options?.crv) {
				case void 0:
				case "Ed25519": return generate("ed25519");
				case "Ed448": return generate("ed448");
				default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported crv option provided, supported values are Ed25519 and Ed448");
			}
			case "ECDH-ES":
			case "ECDH-ES+A128KW":
			case "ECDH-ES+A192KW":
			case "ECDH-ES+A256KW": {
				const crv = options?.crv ?? "P-256";
				switch (crv) {
					case void 0:
					case "P-256":
					case "P-384":
					case "P-521": return generate("ec", { namedCurve: crv });
					case "X25519": return generate("x25519");
					case "X448": return generate("x448");
					default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported crv option provided, supported values are P-256, P-384, P-521, X25519, and X448");
				}
			}
			default: throw new errors_js_1.JOSENotSupported("Invalid or unsupported JWK \"alg\" (Algorithm) Parameter value");
		}
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/key/generate_key_pair.js
var require_generate_key_pair = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.generateKeyPair = generateKeyPair;
	const generate_js_1 = require_generate();
	async function generateKeyPair(alg, options) {
		return (0, generate_js_1.generateKeyPair)(alg, options);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/key/generate_secret.js
var require_generate_secret = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.generateSecret = generateSecret;
	const generate_js_1 = require_generate();
	async function generateSecret(alg, options) {
		return (0, generate_js_1.generateSecret)(alg, options);
	}
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/runtime/runtime.js
var require_runtime$1 = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = "node:crypto";
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/util/runtime.js
var require_runtime = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.default = require_runtime$1().default;
}));
//#endregion
//#region ../node_modules/@vercel/oidc/node_modules/jose/dist/node/cjs/index.js
var require_cjs = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.cryptoRuntime = exports.base64url = exports.generateSecret = exports.generateKeyPair = exports.errors = exports.decodeJwt = exports.decodeProtectedHeader = exports.importJWK = exports.importX509 = exports.importPKCS8 = exports.importSPKI = exports.exportJWK = exports.exportSPKI = exports.exportPKCS8 = exports.UnsecuredJWT = exports.experimental_jwksCache = exports.jwksCache = exports.createRemoteJWKSet = exports.createLocalJWKSet = exports.EmbeddedJWK = exports.calculateJwkThumbprintUri = exports.calculateJwkThumbprint = exports.EncryptJWT = exports.SignJWT = exports.GeneralSign = exports.FlattenedSign = exports.CompactSign = exports.FlattenedEncrypt = exports.CompactEncrypt = exports.jwtDecrypt = exports.jwtVerify = exports.generalVerify = exports.flattenedVerify = exports.compactVerify = exports.GeneralEncrypt = exports.generalDecrypt = exports.flattenedDecrypt = exports.compactDecrypt = void 0;
	var decrypt_js_1 = require_decrypt$2();
	Object.defineProperty(exports, "compactDecrypt", {
		enumerable: true,
		get: function() {
			return decrypt_js_1.compactDecrypt;
		}
	});
	var decrypt_js_2 = require_decrypt$3();
	Object.defineProperty(exports, "flattenedDecrypt", {
		enumerable: true,
		get: function() {
			return decrypt_js_2.flattenedDecrypt;
		}
	});
	var decrypt_js_3 = require_decrypt$1();
	Object.defineProperty(exports, "generalDecrypt", {
		enumerable: true,
		get: function() {
			return decrypt_js_3.generalDecrypt;
		}
	});
	var encrypt_js_1 = require_encrypt$2();
	Object.defineProperty(exports, "GeneralEncrypt", {
		enumerable: true,
		get: function() {
			return encrypt_js_1.GeneralEncrypt;
		}
	});
	var verify_js_1 = require_verify$2();
	Object.defineProperty(exports, "compactVerify", {
		enumerable: true,
		get: function() {
			return verify_js_1.compactVerify;
		}
	});
	var verify_js_2 = require_verify$3();
	Object.defineProperty(exports, "flattenedVerify", {
		enumerable: true,
		get: function() {
			return verify_js_2.flattenedVerify;
		}
	});
	var verify_js_3 = require_verify$1();
	Object.defineProperty(exports, "generalVerify", {
		enumerable: true,
		get: function() {
			return verify_js_3.generalVerify;
		}
	});
	var verify_js_4 = require_verify();
	Object.defineProperty(exports, "jwtVerify", {
		enumerable: true,
		get: function() {
			return verify_js_4.jwtVerify;
		}
	});
	var decrypt_js_4 = require_decrypt();
	Object.defineProperty(exports, "jwtDecrypt", {
		enumerable: true,
		get: function() {
			return decrypt_js_4.jwtDecrypt;
		}
	});
	var encrypt_js_2 = require_encrypt$1();
	Object.defineProperty(exports, "CompactEncrypt", {
		enumerable: true,
		get: function() {
			return encrypt_js_2.CompactEncrypt;
		}
	});
	var encrypt_js_3 = require_encrypt$3();
	Object.defineProperty(exports, "FlattenedEncrypt", {
		enumerable: true,
		get: function() {
			return encrypt_js_3.FlattenedEncrypt;
		}
	});
	var sign_js_1 = require_sign$2();
	Object.defineProperty(exports, "CompactSign", {
		enumerable: true,
		get: function() {
			return sign_js_1.CompactSign;
		}
	});
	var sign_js_2 = require_sign$3();
	Object.defineProperty(exports, "FlattenedSign", {
		enumerable: true,
		get: function() {
			return sign_js_2.FlattenedSign;
		}
	});
	var sign_js_3 = require_sign$1();
	Object.defineProperty(exports, "GeneralSign", {
		enumerable: true,
		get: function() {
			return sign_js_3.GeneralSign;
		}
	});
	var sign_js_4 = require_sign();
	Object.defineProperty(exports, "SignJWT", {
		enumerable: true,
		get: function() {
			return sign_js_4.SignJWT;
		}
	});
	var encrypt_js_4 = require_encrypt();
	Object.defineProperty(exports, "EncryptJWT", {
		enumerable: true,
		get: function() {
			return encrypt_js_4.EncryptJWT;
		}
	});
	var thumbprint_js_1 = require_thumbprint();
	Object.defineProperty(exports, "calculateJwkThumbprint", {
		enumerable: true,
		get: function() {
			return thumbprint_js_1.calculateJwkThumbprint;
		}
	});
	Object.defineProperty(exports, "calculateJwkThumbprintUri", {
		enumerable: true,
		get: function() {
			return thumbprint_js_1.calculateJwkThumbprintUri;
		}
	});
	var embedded_js_1 = require_embedded();
	Object.defineProperty(exports, "EmbeddedJWK", {
		enumerable: true,
		get: function() {
			return embedded_js_1.EmbeddedJWK;
		}
	});
	var local_js_1 = require_local();
	Object.defineProperty(exports, "createLocalJWKSet", {
		enumerable: true,
		get: function() {
			return local_js_1.createLocalJWKSet;
		}
	});
	var remote_js_1 = require_remote();
	Object.defineProperty(exports, "createRemoteJWKSet", {
		enumerable: true,
		get: function() {
			return remote_js_1.createRemoteJWKSet;
		}
	});
	Object.defineProperty(exports, "jwksCache", {
		enumerable: true,
		get: function() {
			return remote_js_1.jwksCache;
		}
	});
	Object.defineProperty(exports, "experimental_jwksCache", {
		enumerable: true,
		get: function() {
			return remote_js_1.experimental_jwksCache;
		}
	});
	var unsecured_js_1 = require_unsecured();
	Object.defineProperty(exports, "UnsecuredJWT", {
		enumerable: true,
		get: function() {
			return unsecured_js_1.UnsecuredJWT;
		}
	});
	var export_js_1 = require_export();
	Object.defineProperty(exports, "exportPKCS8", {
		enumerable: true,
		get: function() {
			return export_js_1.exportPKCS8;
		}
	});
	Object.defineProperty(exports, "exportSPKI", {
		enumerable: true,
		get: function() {
			return export_js_1.exportSPKI;
		}
	});
	Object.defineProperty(exports, "exportJWK", {
		enumerable: true,
		get: function() {
			return export_js_1.exportJWK;
		}
	});
	var import_js_1 = require_import();
	Object.defineProperty(exports, "importSPKI", {
		enumerable: true,
		get: function() {
			return import_js_1.importSPKI;
		}
	});
	Object.defineProperty(exports, "importPKCS8", {
		enumerable: true,
		get: function() {
			return import_js_1.importPKCS8;
		}
	});
	Object.defineProperty(exports, "importX509", {
		enumerable: true,
		get: function() {
			return import_js_1.importX509;
		}
	});
	Object.defineProperty(exports, "importJWK", {
		enumerable: true,
		get: function() {
			return import_js_1.importJWK;
		}
	});
	var decode_protected_header_js_1 = require_decode_protected_header();
	Object.defineProperty(exports, "decodeProtectedHeader", {
		enumerable: true,
		get: function() {
			return decode_protected_header_js_1.decodeProtectedHeader;
		}
	});
	var decode_jwt_js_1 = require_decode_jwt();
	Object.defineProperty(exports, "decodeJwt", {
		enumerable: true,
		get: function() {
			return decode_jwt_js_1.decodeJwt;
		}
	});
	exports.errors = require_errors();
	var generate_key_pair_js_1 = require_generate_key_pair();
	Object.defineProperty(exports, "generateKeyPair", {
		enumerable: true,
		get: function() {
			return generate_key_pair_js_1.generateKeyPair;
		}
	});
	var generate_secret_js_1 = require_generate_secret();
	Object.defineProperty(exports, "generateSecret", {
		enumerable: true,
		get: function() {
			return generate_secret_js_1.generateSecret;
		}
	});
	exports.base64url = require_base64url();
	var runtime_js_1 = require_runtime();
	Object.defineProperty(exports, "cryptoRuntime", {
		enumerable: true,
		get: function() {
			return runtime_js_1.default;
		}
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/verify-vercel-oidc-token.js
var require_verify_vercel_oidc_token = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var verify_vercel_oidc_token_exports = {};
	__export(verify_vercel_oidc_token_exports, { verifyVercelOidcToken: () => verifyVercelOidcToken });
	module.exports = __toCommonJS(verify_vercel_oidc_token_exports);
	var import_jose = require_cjs();
	const VERCEL_OIDC_ISSUER = "https://oidc.vercel.com";
	const VERCEL_OIDC_JWKS_URL = new URL("https://oidc.vercel.com/.well-known/jwks");
	const DEFAULT_ALGORITHMS = ["RS256"];
	const VERCEL_OIDC_JWKS = (0, import_jose.createRemoteJWKSet)(VERCEL_OIDC_JWKS_URL);
	async function verifyVercelOidcToken(token, options) {
		const { algorithms, projectId = process.env.VERCEL_PROJECT_ID, environment = process.env.VERCEL_TARGET_ENV || process.env.VERCEL_ENV, ownerId, ...verifyOptions } = options ?? {};
		if (projectId === "*" && ownerId === void 0 && !hasAudienceVerification(verifyOptions.audience)) throw new TypeError("Expected ownerId or audience to be provided when projectId is '*'.");
		const result = await (0, import_jose.jwtVerify)(token, VERCEL_OIDC_JWKS, {
			...verifyOptions,
			algorithms: algorithms ?? DEFAULT_ALGORITHMS
		});
		validateIssuer(result.payload.iss);
		validateClaim({
			actual: result.payload.project_id,
			claim: "project_id",
			env: "VERCEL_PROJECT_ID",
			expected: projectId,
			option: "projectId"
		});
		validateClaim({
			actual: result.payload.environment,
			claim: "environment",
			env: "VERCEL_TARGET_ENV or VERCEL_ENV",
			expected: environment,
			option: "environment"
		});
		validateOptionalClaim({
			actual: result.payload.owner_id,
			claim: "owner_id",
			expected: ownerId
		});
		return result;
	}
	function hasAudienceVerification(audience) {
		return Array.isArray(audience) ? audience.length > 0 : audience !== void 0;
	}
	function validateIssuer(actual) {
		if (actual !== VERCEL_OIDC_ISSUER && (typeof actual !== "string" || !actual.startsWith(`${VERCEL_OIDC_ISSUER}/`))) throw new TypeError(`Expected Vercel OIDC token iss claim to be "${VERCEL_OIDC_ISSUER}" or to start with "${VERCEL_OIDC_ISSUER}/".`);
	}
	function validateClaim({ actual, claim, env, expected, option }) {
		if (expected === "*") return;
		if (expected === void 0 || expected.length === 0) throw new TypeError(`Expected ${env} to be set or ${option} to be provided. Pass ${option}: '*' to allow any ${claim} claim.`);
		if (Array.isArray(expected) && typeof actual === "string" && expected.includes(actual)) return;
		if (actual !== expected) throw new TypeError(Array.isArray(expected) ? `Expected Vercel OIDC token ${claim} claim to be one of: ${expected.map((value) => `"${value}"`).join(", ")}.` : `Expected Vercel OIDC token ${claim} claim to be "${expected}".`);
	}
	function validateOptionalClaim({ actual, claim, expected }) {
		if (expected === void 0) return;
		if (actual !== expected) throw new TypeError(`Expected Vercel OIDC token ${claim} claim to be "${expected}".`);
	}
	0 && (module.exports = { verifyVercelOidcToken });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/auth-errors.js
var require_auth_errors = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var auth_errors_exports = {};
	__export(auth_errors_exports, {
		AccessTokenMissingError: () => AccessTokenMissingError,
		RefreshAccessTokenFailedError: () => RefreshAccessTokenFailedError
	});
	module.exports = __toCommonJS(auth_errors_exports);
	var AccessTokenMissingError = class extends Error {
		constructor() {
			super("No authentication found. Please log in with the Vercel CLI (vercel login).");
			this.name = "AccessTokenMissingError";
		}
	};
	var RefreshAccessTokenFailedError = class extends Error {
		constructor(cause) {
			super("Failed to refresh authentication token.");
			this.name = "RefreshAccessTokenFailedError";
			if (cause !== void 0) this.cause = cause;
		}
	};
	0 && (module.exports = {
		AccessTokenMissingError,
		RefreshAccessTokenFailedError
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/token-io.js
var require_token_io = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __create = Object.create;
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __getProtoOf = Object.getPrototypeOf;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
		value: mod,
		enumerable: true
	}) : target, mod));
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_io_exports = {};
	__export(token_io_exports, {
		findRootDir: () => findRootDir,
		getUserDataDir: () => getUserDataDir
	});
	module.exports = __toCommonJS(token_io_exports);
	var import_path = __toESM(__require("path"));
	var import_fs = __toESM(__require("fs"));
	var import_os$1 = __toESM(__require("os"));
	var import_token_error = require_token_error();
	function findRootDir() {
		try {
			let dir = process.cwd();
			while (dir !== import_path.default.dirname(dir)) {
				const pkgPath = import_path.default.join(dir, ".vercel");
				if (import_fs.default.existsSync(pkgPath)) return dir;
				dir = import_path.default.dirname(dir);
			}
		} catch (_e) {
			throw new import_token_error.VercelOidcTokenError("Token refresh only supported in node server environments");
		}
		return null;
	}
	function getUserDataDir() {
		if (process.env.XDG_DATA_HOME) return process.env.XDG_DATA_HOME;
		switch (import_os$1.default.platform()) {
			case "darwin": return import_path.default.join(import_os$1.default.homedir(), "Library/Application Support");
			case "linux": return import_path.default.join(import_os$1.default.homedir(), ".local/share");
			case "win32":
				if (process.env.LOCALAPPDATA) return process.env.LOCALAPPDATA;
				return null;
			default: return null;
		}
	}
	0 && (module.exports = {
		findRootDir,
		getUserDataDir
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/oauth.js
var require_oauth = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var oauth_exports = {};
	__export(oauth_exports, {
		processTokenResponse: () => processTokenResponse,
		refreshTokenRequest: () => refreshTokenRequest
	});
	module.exports = __toCommonJS(oauth_exports);
	var import_os = __require("os");
	const VERCEL_ISSUER = "https://vercel.com";
	const VERCEL_CLI_CLIENT_ID = "cl_HYyOPBNtFMfHhaUn9L4QPfTZz6TP47bp";
	const userAgent = `@vercel/oidc node-${process.version} ${(0, import_os.platform)()} (${(0, import_os.arch)()}) ${(0, import_os.hostname)()}`;
	let _tokenEndpoint = null;
	async function getTokenEndpoint() {
		if (_tokenEndpoint) return _tokenEndpoint;
		const response = await fetch(`${VERCEL_ISSUER}/.well-known/openid-configuration`, { headers: { "user-agent": userAgent } });
		if (!response.ok) throw new Error("Failed to discover OAuth endpoints");
		const metadata = await response.json();
		if (!metadata || typeof metadata.token_endpoint !== "string") throw new Error("Invalid OAuth discovery response");
		const endpoint = metadata.token_endpoint;
		_tokenEndpoint = endpoint;
		return endpoint;
	}
	async function refreshTokenRequest(options) {
		const tokenEndpoint = await getTokenEndpoint();
		return await fetch(tokenEndpoint, {
			method: "POST",
			headers: {
				"Content-Type": "application/x-www-form-urlencoded",
				"user-agent": userAgent
			},
			body: new URLSearchParams({
				client_id: VERCEL_CLI_CLIENT_ID,
				grant_type: "refresh_token",
				...options
			})
		});
	}
	async function processTokenResponse(response) {
		const json = await response.json();
		if (!response.ok) {
			const errorMsg = typeof json === "object" && json && "error" in json ? String(json.error) : "Token refresh failed";
			return [new Error(errorMsg)];
		}
		if (typeof json !== "object" || json === null) return [/* @__PURE__ */ new Error("Invalid token response")];
		if (typeof json.access_token !== "string") return [/* @__PURE__ */ new Error("Missing access_token in response")];
		if (json.token_type !== "Bearer") return [/* @__PURE__ */ new Error("Invalid token_type in response")];
		if (typeof json.expires_in !== "number") return [/* @__PURE__ */ new Error("Missing expires_in in response")];
		return [null, json];
	}
	0 && (module.exports = {
		processTokenResponse,
		refreshTokenRequest
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/token-util.js
var require_token_util = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __create = Object.create;
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __getProtoOf = Object.getPrototypeOf;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
		value: mod,
		enumerable: true
	}) : target, mod));
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_util_exports = {};
	__export(token_util_exports, {
		assertVercelOidcTokenResponse: () => assertVercelOidcTokenResponse,
		findProjectInfo: () => findProjectInfo,
		getTokenPayload: () => getTokenPayload,
		getVercelOidcToken: () => getVercelOidcToken,
		getVercelOidcTokenFromCli: () => getVercelOidcTokenFromCli,
		getVercelToken: () => getVercelToken,
		isExpired: () => isExpired,
		loadToken: () => loadToken,
		saveToken: () => saveToken
	});
	module.exports = __toCommonJS(token_util_exports);
	var path = __toESM(__require("path"));
	var fs = __toESM(__require("fs"));
	var import_cli_exec = require_dist$2();
	var import_cli_config = require_dist$3();
	var import_token_error = require_token_error();
	var import_token_io = require_token_io();
	var import_oauth = require_oauth();
	var import_auth_errors = require_auth_errors();
	async function getVercelToken(options) {
		const configDir = (0, import_cli_config.getGlobalPathConfig)();
		const authConfig = (0, import_cli_config.tryReadAuthConfig)(configDir);
		if (!authConfig || !authConfig.token && !authConfig.refreshToken) throw new import_auth_errors.AccessTokenMissingError();
		if (isValidAccessToken(authConfig, options?.expirationBufferMs)) return authConfig.token;
		if (!authConfig.refreshToken) {
			(0, import_cli_config.writeAuthConfig)(configDir, {});
			throw new import_auth_errors.RefreshAccessTokenFailedError("No refresh token available");
		}
		try {
			const tokenResponse = await (0, import_oauth.refreshTokenRequest)({ refresh_token: authConfig.refreshToken });
			const [tokensError, tokens] = await (0, import_oauth.processTokenResponse)(tokenResponse);
			if (tokensError || !tokens) {
				(0, import_cli_config.writeAuthConfig)(configDir, {});
				throw new import_auth_errors.RefreshAccessTokenFailedError(tokensError);
			}
			const updatedConfig = {
				token: tokens.access_token,
				expiresAt: Math.floor(Date.now() / 1e3) + tokens.expires_in,
				refreshToken: tokens.refresh_token
			};
			(0, import_cli_config.writeAuthConfig)(configDir, updatedConfig);
			return updatedConfig.token;
		} catch (error) {
			(0, import_cli_config.writeAuthConfig)(configDir, {});
			if (error instanceof import_auth_errors.AccessTokenMissingError || error instanceof import_auth_errors.RefreshAccessTokenFailedError) throw error;
			throw new import_auth_errors.RefreshAccessTokenFailedError(error);
		}
	}
	function isValidAccessToken(authConfig, expirationBufferMs = 0) {
		if (!authConfig.token) return false;
		if (typeof authConfig.expiresAt !== "number") return true;
		const nowInSeconds = Math.floor(Date.now() / 1e3);
		const bufferInSeconds = expirationBufferMs / 1e3;
		return authConfig.expiresAt >= nowInSeconds + bufferInSeconds;
	}
	async function getVercelOidcTokenFromCli(projectId, teamId) {
		const args = [
			"project",
			"token",
			projectId,
			"--format=json"
		];
		if (teamId) args.push("--scope", teamId);
		try {
			const { stdout } = await (0, import_cli_exec.execVercelCli)(args);
			let parsedOutput;
			if (typeof stdout !== "string") throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token: `vercel project token` did not return stdout");
			try {
				parsedOutput = JSON.parse(stdout);
			} catch {
				throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token: `vercel project token` returned invalid JSON: " + stdout);
			}
			assertVercelOidcTokenResponse(parsedOutput);
			return parsedOutput;
		} catch (error) {
			if (error instanceof import_token_error.VercelOidcTokenError) throw error;
			let message = error instanceof Error ? error.message : "";
			const stderr = error instanceof import_cli_exec.VercelCliError ? error.stderr?.trim() : void 0;
			if (stderr && !message.includes(stderr)) message = `${message}
${stderr}`.trim();
			throw new import_token_error.VercelOidcTokenError(message ? `Failed to refresh OIDC token with the Vercel CLI: ${message}` : "Failed to refresh OIDC token with the Vercel CLI");
		}
	}
	async function getVercelOidcToken(authToken, projectId, teamId) {
		const url = `https://api.vercel.com/v1/projects/${projectId}/token?source=vercel-oidc-refresh${teamId ? `&teamId=${teamId}` : ""}`;
		const res = await fetch(url, {
			method: "POST",
			headers: { Authorization: `Bearer ${authToken}` }
		});
		if (!res.ok) throw new import_token_error.VercelOidcTokenError(`Failed to refresh OIDC token: ${res.statusText}`);
		const tokenRes = await res.json();
		assertVercelOidcTokenResponse(tokenRes);
		return tokenRes;
	}
	function assertVercelOidcTokenResponse(res) {
		if (!res || typeof res !== "object") throw new TypeError("Vercel OIDC token is malformed. Expected an object.");
		if (!("token" in res) || typeof res.token !== "string") throw new TypeError("Vercel OIDC token is malformed. Expected a string-valued token property.");
	}
	function findProjectInfo() {
		const dir = (0, import_token_io.findRootDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find project root directory. Have you linked your project with `vc link?`");
		const prjPath = path.join(dir, ".vercel", "project.json");
		if (!fs.existsSync(prjPath)) throw new import_token_error.VercelOidcTokenError("project.json not found, have you linked your project with `vc link?`");
		const prj = JSON.parse(fs.readFileSync(prjPath, "utf8"));
		if (typeof prj.projectId !== "string" && typeof prj.orgId !== "string") throw new TypeError("Expected a string-valued projectId property. Try running `vc link` to re-link your project.");
		return {
			projectId: prj.projectId,
			teamId: prj.orgId
		};
	}
	function saveToken(token, projectId) {
		const dir = (0, import_token_io.getUserDataDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find user data directory. Please reach out to Vercel support.");
		const tokenPath = path.join(dir, "com.vercel.token", `${projectId}.json`);
		const tokenJson = JSON.stringify(token);
		fs.mkdirSync(path.dirname(tokenPath), {
			mode: 504,
			recursive: true
		});
		fs.writeFileSync(tokenPath, tokenJson);
		fs.chmodSync(tokenPath, 432);
	}
	function loadToken(projectId) {
		const dir = (0, import_token_io.getUserDataDir)();
		if (!dir) throw new import_token_error.VercelOidcTokenError("Unable to find user data directory. Please reach out to Vercel support.");
		const tokenPath = path.join(dir, "com.vercel.token", `${projectId}.json`);
		if (!fs.existsSync(tokenPath)) return null;
		const token = JSON.parse(fs.readFileSync(tokenPath, "utf8"));
		assertVercelOidcTokenResponse(token);
		return token;
	}
	function getTokenPayload(token) {
		const tokenParts = token.split(".");
		if (tokenParts.length !== 3) throw new import_token_error.VercelOidcTokenError("Invalid token.");
		const base64 = tokenParts[1].replace(/-/g, "+").replace(/_/g, "/");
		const padded = base64.padEnd(base64.length + (4 - base64.length % 4) % 4, "=");
		return JSON.parse(Buffer.from(padded, "base64").toString("utf8"));
	}
	function isExpired(token, bufferMs = 0) {
		return token.exp * 1e3 < Date.now() + bufferMs;
	}
	0 && (module.exports = {
		assertVercelOidcTokenResponse,
		findProjectInfo,
		getTokenPayload,
		getVercelOidcToken,
		getVercelOidcTokenFromCli,
		getVercelToken,
		isExpired,
		loadToken,
		saveToken
	});
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/index.js
var require_dist = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var src_exports = {};
	__export(src_exports, {
		AccessTokenMissingError: () => import_auth_errors.AccessTokenMissingError,
		RefreshAccessTokenFailedError: () => import_auth_errors.RefreshAccessTokenFailedError,
		exchangeVercelOidcToken: () => import_exchange_vercel_oidc_token.exchangeVercelOidcToken,
		getContext: () => import_get_context.getContext,
		getVercelOidcToken: () => import_get_vercel_oidc_token_with_refresh.getVercelOidcToken,
		getVercelOidcTokenSync: () => import_get_vercel_oidc_token_sync.getVercelOidcTokenSync,
		getVercelToken: () => import_token_util.getVercelToken,
		verifyVercelOidcToken: () => import_verify_vercel_oidc_token.verifyVercelOidcToken
	});
	module.exports = __toCommonJS(src_exports);
	var import_get_vercel_oidc_token_with_refresh = require_get_vercel_oidc_token_with_refresh();
	var import_get_vercel_oidc_token_sync = require_get_vercel_oidc_token_sync();
	var import_get_context = require_get_context();
	var import_verify_vercel_oidc_token = require_verify_vercel_oidc_token();
	var import_auth_errors = require_auth_errors();
	var import_exchange_vercel_oidc_token = require_exchange_vercel_oidc_token();
	var import_token_util = require_token_util();
	0 && (module.exports = {
		AccessTokenMissingError,
		RefreshAccessTokenFailedError,
		exchangeVercelOidcToken,
		getContext,
		getVercelOidcToken,
		getVercelOidcTokenSync,
		getVercelToken,
		verifyVercelOidcToken
	});
}));
//#endregion
//#region ../node_modules/@ai-sdk/gateway/node_modules/@vercel/oidc/dist/token.js
var require_token$2 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_exports = {};
	__export(token_exports, { refreshToken: () => refreshToken });
	module.exports = __toCommonJS(token_exports);
	var import_token_error = require_token_error$2();
	var import_token_util = require_token_util$2();
	async function refreshToken(options) {
		let projectId = options?.project;
		let teamId = options?.team;
		if (!projectId && !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectInfo.projectId;
			teamId = projectInfo.teamId;
		} else if (!projectId || !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectId ?? projectInfo.projectId;
			teamId = teamId ?? projectInfo.teamId;
		}
		if (!projectId) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token: No project specified. Try re-linking your project with `vc link`");
		let maybeToken = (0, import_token_util.loadToken)(projectId);
		if (!maybeToken || (0, import_token_util.isExpired)((0, import_token_util.getTokenPayload)(maybeToken.token), options?.expirationBufferMs)) {
			const authToken = await (0, import_token_util.getVercelToken)({ expirationBufferMs: options?.expirationBufferMs });
			maybeToken = await (0, import_token_util.getVercelOidcToken)(authToken, projectId, teamId);
			if (!maybeToken) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token");
			(0, import_token_util.saveToken)(maybeToken, projectId);
		}
		process.env.VERCEL_OIDC_TOKEN = maybeToken.token;
	}
	0 && (module.exports = { refreshToken });
}));
//#endregion
//#region ../node_modules/@vercel/oidc/dist/token.js
var require_token$1 = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_exports = {};
	__export(token_exports, { refreshToken: () => refreshToken });
	module.exports = __toCommonJS(token_exports);
	var import_cli_config = require_dist$3();
	var import_token_error = require_token_error();
	var import_token_util = require_token_util();
	async function refreshToken(options) {
		let projectId = options?.project;
		let teamId = options?.team;
		if (!projectId && !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectInfo.projectId;
			teamId = projectInfo.teamId;
		} else if (!projectId || !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectId ?? projectInfo.projectId;
			teamId = teamId ?? projectInfo.teamId;
		}
		if (!projectId) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token: No project specified. Try re-linking your project with `vc link`");
		let maybeToken = (0, import_token_util.loadToken)(projectId);
		if (!maybeToken || (0, import_token_util.isExpired)((0, import_token_util.getTokenPayload)(maybeToken.token), options?.expirationBufferMs)) {
			const configDir = (0, import_cli_config.getGlobalPathConfig)();
			if ((0, import_cli_config.getLikelyEffectiveCredStorage)(configDir) === "keyring") maybeToken = await (0, import_token_util.getVercelOidcTokenFromCli)(projectId, teamId);
			else {
				const authToken = await (0, import_token_util.getVercelToken)({ expirationBufferMs: options?.expirationBufferMs });
				maybeToken = await (0, import_token_util.getVercelOidcToken)(authToken, projectId, teamId);
			}
			if (!maybeToken) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token");
			(0, import_token_util.saveToken)(maybeToken, projectId);
		}
		process.env.VERCEL_OIDC_TOKEN = maybeToken.token;
	}
	0 && (module.exports = { refreshToken });
}));
//#endregion
//#region ../node_modules/@workflow/world-vercel/node_modules/@vercel/oidc/dist/token.js
var require_token = /* @__PURE__ */ __commonJSMin(((exports, module) => {
	var __defProp = Object.defineProperty;
	var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
	var __getOwnPropNames = Object.getOwnPropertyNames;
	var __hasOwnProp = Object.prototype.hasOwnProperty;
	var __export = (target, all) => {
		for (var name in all) __defProp(target, name, {
			get: all[name],
			enumerable: true
		});
	};
	var __copyProps = (to, from, except, desc) => {
		if (from && typeof from === "object" || typeof from === "function") {
			for (let key of __getOwnPropNames(from)) if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
				get: () => from[key],
				enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
			});
		}
		return to;
	};
	var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
	var token_exports = {};
	__export(token_exports, { refreshToken: () => refreshToken });
	module.exports = __toCommonJS(token_exports);
	var import_token_error = require_token_error$1();
	var import_token_util = require_token_util$1();
	async function refreshToken(options) {
		let projectId = options?.project;
		let teamId = options?.team;
		if (!projectId && !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectInfo.projectId;
			teamId = projectInfo.teamId;
		} else if (!projectId || !teamId) {
			const projectInfo = (0, import_token_util.findProjectInfo)();
			projectId = projectId ?? projectInfo.projectId;
			teamId = teamId ?? projectInfo.teamId;
		}
		if (!projectId) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token: No project specified. Try re-linking your project with `vc link`");
		let maybeToken = (0, import_token_util.loadToken)(projectId);
		if (!maybeToken || (0, import_token_util.isExpired)((0, import_token_util.getTokenPayload)(maybeToken.token), options?.expirationBufferMs)) {
			const authToken = await (0, import_token_util.getVercelToken)({ expirationBufferMs: options?.expirationBufferMs });
			maybeToken = await (0, import_token_util.getVercelOidcToken)(authToken, projectId, teamId);
			if (!maybeToken) throw new import_token_error.VercelOidcTokenError("Failed to refresh OIDC token");
			(0, import_token_util.saveToken)(maybeToken, projectId);
		}
		process.env.VERCEL_OIDC_TOKEN = maybeToken.token;
	}
	0 && (module.exports = { refreshToken });
}));
//#endregion
export { require_dist as n, require_dist$1 as r, require_token$2 as t };
