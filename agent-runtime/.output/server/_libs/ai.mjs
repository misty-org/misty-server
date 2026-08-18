import { createRequire as __wkfCreateRequire } from "node:module";
if (typeof globalThis.require === "undefined") globalThis.require = __wkfCreateRequire(import.meta.url);
import { A as _null, B as object, C as safeParseJSON, D as zodSchema, E as withUserAgentSuffix, F as lazy, G as AISDKError, H as string, I as literal, J as getErrorMessage, K as InvalidPromptError, L as looseObject, M as boolean, N as custom, O as _enum, P as discriminatedUnion, R as never, S as resolve, U as union, V as record, W as unknown, _ as isProviderReference, a as asArray, b as lazySchema, c as convertBase64ToUint8Array, d as detectMediaType, f as fetchWithValidatedRedirects, g as isFullMediaType, h as isBuffer, i as DownloadError, j as array, k as _instanceof, l as convertUint8ArrayToBase64, o as asSchema, p as getRuntimeEnvironmentUserAgent, q as TypeValidationError, s as cancelResponseBody, t as gateway, u as createIdGenerator, v as isUrlSupported, w as safeValidateTypes, x as readResponseWithSizeLimit, z as number } from "./@ai-sdk/gateway+[...].mjs";
//#region ../node_modules/ai/dist/index.js
var __defProp = Object.defineProperty;
var __export = (target, all) => {
	for (var name23 in all) __defProp(target, name23, {
		get: all[name23],
		enumerable: true
	});
};
var name5 = "AI_InvalidToolInputError";
var marker5 = `vercel.ai.error.${name5}`;
var symbol5 = Symbol.for(marker5);
var _a5;
var InvalidToolInputError = class extends AISDKError {
	constructor({ toolInput, toolName, cause, message = `Invalid input for tool ${toolName}: ${getErrorMessage(cause)}` }) {
		super({
			name: name5,
			message,
			cause
		});
		this[_a5] = true;
		this.toolInput = toolInput;
		this.toolName = toolName;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker5);
	}
};
_a5 = symbol5;
var name6 = "AI_ToolCallNotFoundForApprovalError";
var marker6 = `vercel.ai.error.${name6}`;
var symbol6 = Symbol.for(marker6);
var _a6;
var ToolCallNotFoundForApprovalError = class extends AISDKError {
	constructor({ toolCallId, approvalId }) {
		super({
			name: name6,
			message: `Tool call "${toolCallId}" not found for approval request "${approvalId}".`
		});
		this[_a6] = true;
		this.toolCallId = toolCallId;
		this.approvalId = approvalId;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker6);
	}
};
_a6 = symbol6;
var name7 = "AI_MissingToolResultsError";
var marker7 = `vercel.ai.error.${name7}`;
var symbol7 = Symbol.for(marker7);
var _a7;
var MissingToolResultsError = class extends AISDKError {
	constructor({ toolCallIds }) {
		super({
			name: name7,
			message: `Tool result${toolCallIds.length > 1 ? "s are" : " is"} missing for tool call${toolCallIds.length > 1 ? "s" : ""} ${toolCallIds.join(", ")}.`
		});
		this[_a7] = true;
		this.toolCallIds = toolCallIds;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker7);
	}
};
_a7 = symbol7;
var name9 = "AI_NoObjectGeneratedError";
var marker9 = `vercel.ai.error.${name9}`;
var symbol9 = Symbol.for(marker9);
var _a9;
var NoObjectGeneratedError = class extends AISDKError {
	constructor({ message = "No object generated.", cause, text: text2, response, usage, finishReason }) {
		super({
			name: name9,
			message,
			cause
		});
		this[_a9] = true;
		this.text = text2;
		this.response = response;
		this.usage = usage;
		this.finishReason = finishReason;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker9);
	}
};
_a9 = symbol9;
var name15 = "AI_NoSuchToolError";
var marker15 = `vercel.ai.error.${name15}`;
var symbol15 = Symbol.for(marker15);
var _a15;
var NoSuchToolError = class extends AISDKError {
	constructor({ toolName, availableTools = void 0, message = `Model tried to call unavailable tool '${toolName}'. ${availableTools === void 0 ? "No tools are available." : `Available tools: ${availableTools.join(", ")}.`}` }) {
		super({
			name: name15,
			message
		});
		this[_a15] = true;
		this.toolName = toolName;
		this.availableTools = availableTools;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker15);
	}
};
_a15 = symbol15;
var name16 = "AI_ToolCallRepairError";
var marker16 = `vercel.ai.error.${name16}`;
var symbol16 = Symbol.for(marker16);
var _a16;
var ToolCallRepairError = class extends AISDKError {
	constructor({ cause, originalError, message = `Error repairing tool call: ${getErrorMessage(cause)}` }) {
		super({
			name: name16,
			message,
			cause
		});
		this[_a16] = true;
		this.originalError = originalError;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker16);
	}
};
_a16 = symbol16;
var UnsupportedModelVersionError = class extends AISDKError {
	constructor(options) {
		super({
			name: "AI_UnsupportedModelVersionError",
			message: `Unsupported model version ${options.version} for provider "${options.provider}" and model "${options.modelId}". AI SDK 5 only supports models that implement specification version "v2".`
		});
		this.version = options.version;
		this.provider = options.provider;
		this.modelId = options.modelId;
	}
};
var name18 = "AI_InvalidDataContentError";
var marker18 = `vercel.ai.error.${name18}`;
var symbol18 = Symbol.for(marker18);
var _a18;
var InvalidDataContentError = class extends AISDKError {
	constructor({ content, cause, message = `Invalid data content. Expected a base64 string, Uint8Array, ArrayBuffer, or Buffer, but got ${typeof content}.` }) {
		super({
			name: name18,
			message,
			cause
		});
		this[_a18] = true;
		this.content = content;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker18);
	}
};
_a18 = symbol18;
var name19 = "AI_InvalidMessageRoleError";
var marker19 = `vercel.ai.error.${name19}`;
var symbol19 = Symbol.for(marker19);
var _a19;
var InvalidMessageRoleError = class extends AISDKError {
	constructor({ role, message = `Invalid message role: '${role}'. Must be one of: "system", "user", "assistant", "tool".` }) {
		super({
			name: name19,
			message
		});
		this[_a19] = true;
		this.role = role;
	}
	static isInstance(error) {
		return AISDKError.hasMarker(error, marker19);
	}
};
_a19 = symbol19;
function formatWarning({ warning, provider, model }) {
	const prefix = `AI SDK Warning${provider != null && model != null ? ` (${provider} / ${model})` : ""}:`;
	switch (warning.type) {
		case "unsupported": {
			let message = `${prefix} The feature "${warning.feature}" is not supported.`;
			if (warning.details) message += ` ${warning.details}`;
			return message;
		}
		case "compatibility": {
			let message = `${prefix} The feature "${warning.feature}" is used in a compatibility mode.`;
			if (warning.details) message += ` ${warning.details}`;
			return message;
		}
		case "deprecated": return `${prefix} Deprecated: "${warning.setting}". ${warning.message}`;
		case "other": return `${prefix} ${warning.message}`;
		default: return `${prefix} ${JSON.stringify(warning, null, 2)}`;
	}
}
var FIRST_WARNING_INFO_MESSAGE = "AI SDK Warning System: To turn off warning logging, set the AI_SDK_LOG_WARNINGS global to false.";
var hasLoggedBefore = false;
function emitWarning({ message, type }) {
	if (typeof process !== "undefined" && typeof process.emitWarning === "function") process.emitWarning(message, { type });
	else console.warn(message);
}
var logWarnings = (options) => {
	if (options.warnings.length === 0) return;
	const logger = globalThis.AI_SDK_LOG_WARNINGS;
	if (logger === false) return;
	if (typeof logger === "function") {
		logger(options);
		return;
	}
	if (!hasLoggedBefore) {
		hasLoggedBefore = true;
		emitWarning({
			message: FIRST_WARNING_INFO_MESSAGE,
			type: "Warning"
		});
	}
	for (const warning of options.warnings) emitWarning({
		message: formatWarning({
			warning,
			provider: options.provider,
			model: options.model
		}),
		type: warning.type === "deprecated" ? "DeprecationWarning" : "Warning"
	});
};
function logV2CompatibilityWarning({ provider, modelId }) {
	logWarnings({
		warnings: [{
			type: "compatibility",
			feature: "specificationVersion",
			details: `Using v2 specification compatibility mode. Some features may not be available.`
		}],
		provider,
		model: modelId
	});
}
function asEmbeddingModelV3(model) {
	if (model.specificationVersion === "v3") return model;
	logV2CompatibilityWarning({
		provider: model.provider,
		modelId: model.modelId
	});
	return new Proxy(model, { get(target, prop) {
		if (prop === "specificationVersion") return "v3";
		return target[prop];
	} });
}
function asEmbeddingModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	const v3Model = model.specificationVersion === "v2" ? asEmbeddingModelV3(model) : model;
	return new Proxy(v3Model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asImageModelV3(model) {
	if (model.specificationVersion === "v3") return model;
	logV2CompatibilityWarning({
		provider: model.provider,
		modelId: model.modelId
	});
	return new Proxy(model, { get(target, prop) {
		if (prop === "specificationVersion") return "v3";
		return target[prop];
	} });
}
function asImageModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	const v3Model = model.specificationVersion === "v2" ? asImageModelV3(model) : model;
	return new Proxy(v3Model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asLanguageModelV3(model) {
	if (model.specificationVersion === "v3") return model;
	logV2CompatibilityWarning({
		provider: model.provider,
		modelId: model.modelId
	});
	return new Proxy(model, { get(target, prop) {
		switch (prop) {
			case "specificationVersion": return "v3";
			case "doGenerate": return async (...args) => {
				const result = await target.doGenerate(...args);
				return {
					...result,
					finishReason: convertV2FinishReasonToV3(result.finishReason),
					usage: convertV2UsageToV3(result.usage)
				};
			};
			case "doStream": return async (...args) => {
				const result = await target.doStream(...args);
				return {
					...result,
					stream: convertV2StreamToV3(result.stream)
				};
			};
			default: return target[prop];
		}
	} });
}
function convertV2StreamToV3(stream) {
	return stream.pipeThrough(new TransformStream({ transform(chunk, controller) {
		switch (chunk.type) {
			case "finish":
				controller.enqueue({
					...chunk,
					finishReason: convertV2FinishReasonToV3(chunk.finishReason),
					usage: convertV2UsageToV3(chunk.usage)
				});
				break;
			default: controller.enqueue(chunk);
		}
	} }));
}
function convertV2FinishReasonToV3(finishReason) {
	return {
		unified: finishReason === "unknown" ? "other" : finishReason,
		raw: void 0
	};
}
function convertV2UsageToV3(usage) {
	return {
		inputTokens: {
			total: usage.inputTokens,
			noCache: void 0,
			cacheRead: usage.cachedInputTokens,
			cacheWrite: void 0
		},
		outputTokens: {
			total: usage.outputTokens,
			text: void 0,
			reasoning: usage.reasoningTokens
		}
	};
}
function asLanguageModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	const v3Model = model.specificationVersion === "v2" ? asLanguageModelV3(model) : model;
	return new Proxy(v3Model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asRerankingModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	return new Proxy(model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asSpeechModelV3(model) {
	if (model.specificationVersion === "v3") return model;
	logV2CompatibilityWarning({
		provider: model.provider,
		modelId: model.modelId
	});
	return new Proxy(model, { get(target, prop) {
		if (prop === "specificationVersion") return "v3";
		return target[prop];
	} });
}
function asSpeechModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	const v3Model = model.specificationVersion === "v2" ? asSpeechModelV3(model) : model;
	return new Proxy(v3Model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asTranscriptionModelV3(model) {
	if (model.specificationVersion === "v3") return model;
	logV2CompatibilityWarning({
		provider: model.provider,
		modelId: model.modelId
	});
	return new Proxy(model, { get(target, prop) {
		if (prop === "specificationVersion") return "v3";
		return target[prop];
	} });
}
function asTranscriptionModelV4(model) {
	if (model.specificationVersion === "v4") return model;
	const v3Model = model.specificationVersion === "v2" ? asTranscriptionModelV3(model) : model;
	return new Proxy(v3Model, { get(target, prop) {
		if (prop === "specificationVersion") return "v4";
		return target[prop];
	} });
}
function asProviderV3(provider) {
	if ("specificationVersion" in provider && provider.specificationVersion === "v3") return provider;
	const v2Provider = provider;
	return {
		specificationVersion: "v3",
		languageModel: (modelId) => asLanguageModelV3(v2Provider.languageModel(modelId)),
		embeddingModel: (modelId) => asEmbeddingModelV3(v2Provider.textEmbeddingModel(modelId)),
		imageModel: (modelId) => asImageModelV3(v2Provider.imageModel(modelId)),
		transcriptionModel: v2Provider.transcriptionModel ? (modelId) => asTranscriptionModelV3(v2Provider.transcriptionModel(modelId)) : void 0,
		speechModel: v2Provider.speechModel ? (modelId) => asSpeechModelV3(v2Provider.speechModel(modelId)) : void 0,
		rerankingModel: void 0
	};
}
function asProviderV4(provider) {
	if ("specificationVersion" in provider && provider.specificationVersion === "v4") return provider;
	const v3Provider = !("specificationVersion" in provider) || provider.specificationVersion !== "v3" ? asProviderV3(provider) : provider;
	return {
		specificationVersion: "v4",
		languageModel: (modelId) => asLanguageModelV4(v3Provider.languageModel(modelId)),
		embeddingModel: (modelId) => asEmbeddingModelV4(v3Provider.embeddingModel(modelId)),
		imageModel: (modelId) => asImageModelV4(v3Provider.imageModel(modelId)),
		transcriptionModel: v3Provider.transcriptionModel ? (modelId) => asTranscriptionModelV4(v3Provider.transcriptionModel(modelId)) : void 0,
		speechModel: v3Provider.speechModel ? (modelId) => asSpeechModelV4(v3Provider.speechModel(modelId)) : void 0,
		rerankingModel: v3Provider.rerankingModel ? (modelId) => asRerankingModelV4(v3Provider.rerankingModel(modelId)) : void 0
	};
}
function resolveLanguageModel(model) {
	if (typeof model === "string") return getGlobalProvider().languageModel(model);
	if (![
		"v4",
		"v3",
		"v2"
	].includes(model.specificationVersion)) {
		const unsupportedModel = model;
		throw new UnsupportedModelVersionError({
			version: unsupportedModel.specificationVersion,
			provider: unsupportedModel.provider,
			modelId: unsupportedModel.modelId
		});
	}
	return asLanguageModelV4(model);
}
function getGlobalProvider() {
	var _a23;
	return asProviderV4((_a23 = globalThis.AI_SDK_DEFAULT_PROVIDER) != null ? _a23 : gateway);
}
var VERSION = "7.0.66";
var download = async ({ url, maxBytes, abortSignal }) => {
	var _a23;
	const urlText = url.toString();
	try {
		const headers = withUserAgentSuffix({}, `ai-sdk/${VERSION}`, getRuntimeEnvironmentUserAgent());
		const response = await fetchWithValidatedRedirects({
			url: urlText,
			headers,
			abortSignal
		});
		if (!response.ok) {
			await cancelResponseBody(response);
			throw new DownloadError({
				url: urlText,
				statusCode: response.status,
				statusText: response.statusText
			});
		}
		return {
			data: await readResponseWithSizeLimit({
				response,
				url: urlText,
				maxBytes: maxBytes != null ? maxBytes : 2147483648
			}),
			mediaType: (_a23 = response.headers.get("content-type")) != null ? _a23 : void 0
		};
	} catch (error) {
		if (DownloadError.isInstance(error)) throw error;
		throw new DownloadError({
			url: urlText,
			cause: error
		});
	}
};
var createDefaultDownloadFunction = (download2 = download) => (requestedDownloads) => Promise.all(requestedDownloads.map(async (requestedDownload) => requestedDownload.isUrlSupportedByModel ? null : await download2(requestedDownload)));
function mergeObjects(base, overrides) {
	if (base === void 0 && overrides === void 0) return;
	if (base === void 0) return overrides;
	if (overrides === void 0) return base;
	const result = { ...base };
	for (const key in overrides) {
		if (key === "__proto__" || key === "constructor" || key === "prototype") continue;
		if (Object.prototype.hasOwnProperty.call(overrides, key)) {
			const overridesValue = overrides[key];
			if (overridesValue === void 0) continue;
			const baseValue = key in base ? base[key] : void 0;
			const isSourceObject = overridesValue !== null && typeof overridesValue === "object" && !Array.isArray(overridesValue) && !(overridesValue instanceof Date) && !(overridesValue instanceof RegExp);
			const isTargetObject = baseValue !== null && baseValue !== void 0 && typeof baseValue === "object" && !Array.isArray(baseValue) && !(baseValue instanceof Date) && !(baseValue instanceof RegExp);
			if (isSourceObject && isTargetObject) result[key] = mergeObjects(baseValue, overridesValue);
			else result[key] = overridesValue;
		}
	}
	return result;
}
function splitDataUrl(dataUrl) {
	try {
		const [header, base64Content] = dataUrl.split(",");
		return {
			mediaType: header.split(";")[0].split(":")[1],
			base64Content
		};
	} catch (e) {
		return {
			mediaType: void 0,
			base64Content: void 0
		};
	}
}
function isTaggedFileData(value) {
	if (typeof value !== "object" || value === null) return false;
	const type = value.type;
	return type === "data" || type === "url" || type === "reference" || type === "text";
}
function convertUrlToFilePartData(url) {
	if (url.protocol === "data:") {
		const { mediaType, base64Content } = splitDataUrl(url.toString());
		if (mediaType == null || base64Content == null) throw new InvalidDataContentError({
			content: url,
			message: `Invalid data URL format in content ${url.toString()}`
		});
		return {
			data: {
				type: "data",
				data: base64Content
			},
			mediaType
		};
	}
	return {
		data: {
			type: "url",
			url
		},
		mediaType: void 0
	};
}
function convertInlineDataToFilePartData(content) {
	if (content instanceof Uint8Array) return {
		data: {
			type: "data",
			data: content
		},
		mediaType: void 0
	};
	if (content instanceof ArrayBuffer) return {
		data: {
			type: "data",
			data: new Uint8Array(content)
		},
		mediaType: void 0
	};
	if (isBuffer(content)) return {
		data: {
			type: "data",
			data: new Uint8Array(content)
		},
		mediaType: void 0
	};
	return {
		data: {
			type: "data",
			data: content
		},
		mediaType: void 0
	};
}
function convertToLanguageModelV4FilePart(content) {
	if (isTaggedFileData(content)) switch (content.type) {
		case "data":
			if (typeof content.data === "string" && content.data.startsWith("data:")) throw new InvalidDataContentError({
				content: content.data,
				message: "Data URLs are not valid inline data. Pass them as { type: \"url\", url } instead."
			});
			return convertInlineDataToFilePartData(content.data);
		case "url": return convertUrlToFilePartData(content.url);
		case "reference": return {
			data: {
				type: "reference",
				reference: content.reference
			},
			mediaType: void 0
		};
		case "text": return {
			data: {
				type: "text",
				text: content.text
			},
			mediaType: void 0
		};
	}
	if (content instanceof URL) return convertUrlToFilePartData(content);
	if (typeof content === "string") try {
		return convertUrlToFilePartData(new URL(content));
	} catch (e) {
		return convertInlineDataToFilePartData(content);
	}
	if (isProviderReference(content)) return {
		data: {
			type: "reference",
			reference: content
		},
		mediaType: void 0
	};
	return convertInlineDataToFilePartData(content);
}
async function convertToLanguageModelPrompt({ prompt, supportedUrls, download: download2 = createDefaultDownloadFunction(), provider }) {
	const downloadedAssets = await downloadAssets(prompt.messages, download2, supportedUrls);
	const approvalIdToToolCallId = /* @__PURE__ */ new Map();
	for (const message of prompt.messages) if (message.role === "assistant" && Array.isArray(message.content)) {
		for (const part of message.content) if (part.type === "tool-approval-request" && "approvalId" in part && "toolCallId" in part) approvalIdToToolCallId.set(part.approvalId, part.toolCallId);
	}
	const approvedToolCallIds = /* @__PURE__ */ new Set();
	for (const message of prompt.messages) if (message.role === "tool") {
		for (const part of message.content) if (part.type === "tool-approval-response") {
			const toolCallId = approvalIdToToolCallId.get(part.approvalId);
			if (toolCallId) approvedToolCallIds.add(toolCallId);
		}
	}
	const messages = [...prompt.instructions != null ? typeof prompt.instructions === "string" ? [{
		role: "system",
		content: prompt.instructions
	}] : asArray(prompt.instructions).map((message) => ({
		role: "system",
		content: message.content,
		providerOptions: message.providerOptions
	})) : [], ...prompt.messages.map((message) => convertToLanguageModelMessage({
		message,
		downloadedAssets,
		provider
	}))];
	const combinedMessages = [];
	for (const message of messages) {
		if (message.role !== "tool") {
			combinedMessages.push(message);
			continue;
		}
		const lastCombinedMessage = combinedMessages.at(-1);
		if ((lastCombinedMessage == null ? void 0 : lastCombinedMessage.role) === "tool") {
			const lastContentPart = lastCombinedMessage.content.at(-1);
			if (lastContentPart != null && lastCombinedMessage.providerOptions != null) lastContentPart.providerOptions = mergeObjects(lastCombinedMessage.providerOptions, lastContentPart.providerOptions);
			lastCombinedMessage.content.push(...message.content);
			lastCombinedMessage.providerOptions = message.providerOptions;
		} else combinedMessages.push(message);
	}
	const toolCallIds = /* @__PURE__ */ new Set();
	for (const message of combinedMessages) switch (message.role) {
		case "assistant":
			for (const content of message.content) if (content.type === "tool-call" && !content.providerExecuted) toolCallIds.add(content.toolCallId);
			break;
		case "tool":
			for (const content of message.content) if (content.type === "tool-result") toolCallIds.delete(content.toolCallId);
			break;
		case "user":
		case "system":
			for (const id of approvedToolCallIds) toolCallIds.delete(id);
			if (toolCallIds.size > 0) throw new MissingToolResultsError({ toolCallIds: Array.from(toolCallIds) });
	}
	for (const id of approvedToolCallIds) toolCallIds.delete(id);
	if (toolCallIds.size > 0) throw new MissingToolResultsError({ toolCallIds: Array.from(toolCallIds) });
	return combinedMessages.filter((message) => message.role !== "tool" || message.content.length > 0);
}
function convertToLanguageModelMessage({ message, downloadedAssets, provider }) {
	const warnings = [];
	const role = message.role;
	switch (role) {
		case "system": return {
			role: "system",
			content: message.content,
			providerOptions: message.providerOptions
		};
		case "user": {
			if (typeof message.content === "string") return {
				role: "user",
				content: [{
					type: "text",
					text: message.content
				}],
				providerOptions: message.providerOptions
			};
			const converted = {
				role: "user",
				content: message.content.map((part) => {
					if (part.type === "image") warnings.push({
						type: "deprecated",
						setting: "\"image\" content part",
						message: `The "image" content part type is deprecated. Use a "file" part with mediaType: 'image' (or a more specific image/* subtype) instead.`
					});
					return convertImagePartToFilePart(part);
				}).map((part) => convertPartToLanguageModelPart(part, downloadedAssets)).filter((part) => part.type !== "text" || part.text !== ""),
				providerOptions: message.providerOptions
			};
			if (warnings.length > 0) logWarnings({ warnings });
			return converted;
		}
		case "assistant": {
			if (typeof message.content === "string") return {
				role: "assistant",
				content: [{
					type: "text",
					text: message.content
				}],
				providerOptions: message.providerOptions
			};
			const converted = {
				role: "assistant",
				content: message.content.filter((part) => part.type !== "text" || part.text !== "" || part.providerOptions != null).filter((part) => part.type !== "tool-approval-request").map((part) => {
					const providerOptions = part.providerOptions;
					switch (part.type) {
						case "custom": return {
							type: "custom",
							kind: part.kind,
							providerOptions
						};
						case "file": {
							const { data, mediaType } = convertToLanguageModelV4FilePart(part.data);
							return {
								type: "file",
								data,
								filename: part.filename,
								mediaType: mediaType != null ? mediaType : part.mediaType,
								providerOptions
							};
						}
						case "reasoning": return {
							type: "reasoning",
							text: part.text,
							providerOptions
						};
						case "reasoning-file": {
							const { data, mediaType } = convertToLanguageModelV4FilePart(part.data);
							if (data.type !== "data" && data.type !== "url") throw new Error(`Unsupported reasoning-file data type: ${data.type}`);
							return {
								type: "reasoning-file",
								data,
								mediaType: mediaType != null ? mediaType : part.mediaType,
								providerOptions
							};
						}
						case "text": return {
							type: "text",
							text: part.text,
							providerOptions
						};
						case "tool-call": return {
							type: "tool-call",
							toolCallId: part.toolCallId,
							toolName: part.toolName,
							input: part.input,
							providerExecuted: part.providerExecuted,
							providerOptions
						};
						case "tool-result": return {
							type: "tool-result",
							toolCallId: part.toolCallId,
							toolName: part.toolName,
							output: mapToolResultOutput({
								output: part.output,
								provider,
								warnings,
								downloadedAssets
							}),
							providerOptions
						};
					}
				}),
				providerOptions: message.providerOptions
			};
			if (warnings.length > 0) logWarnings({ warnings });
			return converted;
		}
		case "tool": {
			const converted = {
				role: "tool",
				content: message.content.filter((part) => part.type !== "tool-approval-response" || part.providerExecuted).map((part) => {
					switch (part.type) {
						case "tool-result": return {
							type: "tool-result",
							toolCallId: part.toolCallId,
							toolName: part.toolName,
							output: mapToolResultOutput({
								output: part.output,
								provider,
								warnings,
								downloadedAssets
							}),
							providerOptions: part.providerOptions
						};
						case "tool-approval-response": return {
							type: "tool-approval-response",
							approvalId: part.approvalId,
							approved: part.approved,
							reason: part.reason
						};
					}
				}),
				providerOptions: message.providerOptions
			};
			if (warnings.length > 0) logWarnings({ warnings });
			return converted;
		}
		default: throw new InvalidMessageRoleError({ role });
	}
}
function convertImagePartToFilePart(part) {
	var _a23;
	if (part.type !== "image") return part;
	return {
		type: "file",
		data: part.image,
		mediaType: (_a23 = part.mediaType) != null ? _a23 : "image",
		providerOptions: part.providerOptions
	};
}
async function downloadAssets(messages, download2, supportedUrls) {
	const downloadableFiles = [];
	for (const message of messages) {
		if (message.role === "user" && Array.isArray(message.content)) for (const part of message.content) {
			const filePart = convertImagePartToFilePart(part);
			if (filePart.type === "file") downloadableFiles.push(filePart);
		}
		if (message.role === "tool") for (const part of message.content) {
			if (part.type !== "tool-result") continue;
			if (part.output.type !== "content") continue;
			for (const contentPart of part.output.value) if (contentPart.type === "file") downloadableFiles.push(contentPart);
		}
		if (message.role === "assistant" && Array.isArray(message.content)) for (const part of message.content) {
			if (part.type !== "tool-result") continue;
			if (part.output.type !== "content") continue;
			for (const contentPart of part.output.value) if (contentPart.type === "file") downloadableFiles.push(contentPart);
		}
	}
	const plannedDownloads = downloadableFiles.map((part) => {
		const mediaType = part.mediaType;
		const { data } = convertToLanguageModelV4FilePart(part.data);
		return {
			mediaType,
			data
		};
	}).filter((part) => part.data.type === "url").map((part) => ({
		url: part.data.url,
		isUrlSupportedByModel: part.mediaType != null && isUrlSupported({
			url: part.data.url.toString(),
			mediaType: part.mediaType,
			supportedUrls
		})
	}));
	const downloadedFiles = await download2(plannedDownloads);
	return Object.fromEntries(downloadedFiles.map((file, index) => file == null ? null : [plannedDownloads[index].url.toString(), {
		data: file.data,
		mediaType: file.mediaType
	}]).filter((file) => file != null));
}
function convertPartToLanguageModelPart(part, downloadedAssets) {
	if (part.type === "text") return {
		type: "text",
		text: part.text,
		providerOptions: part.providerOptions
	};
	const { data: normalizedData, mediaType: dataUrlMediaType } = convertToLanguageModelV4FilePart(part.data);
	let mediaType = dataUrlMediaType != null ? dataUrlMediaType : part.mediaType;
	let data = normalizedData;
	if (data.type === "url") {
		const downloadedFile = downloadedAssets[data.url.toString()];
		if (downloadedFile) {
			data = {
				type: "data",
				data: downloadedFile.data
			};
			if (downloadedFile.mediaType != null && (mediaType == null || !isFullMediaType(mediaType))) mediaType = downloadedFile.mediaType;
		}
	}
	if (data.type === "data" && (data.data instanceof Uint8Array || typeof data.data === "string")) {
		const imageMediaType = detectMediaType({
			data: data.data,
			topLevelType: "image"
		});
		if (imageMediaType != null) mediaType = imageMediaType;
	}
	if (mediaType == null) throw new Error(`Media type is missing for file part`);
	return {
		type: "file",
		mediaType,
		filename: part.filename,
		data,
		providerOptions: part.providerOptions
	};
}
function mapToolResultOutput({ output, provider, warnings = [], downloadedAssets }) {
	if (output.type !== "content") return output;
	return {
		type: "content",
		value: output.value.map((item) => {
			var _a23;
			switch (item.type) {
				case "file": {
					const convertedPart = convertPartToLanguageModelPart(item, downloadedAssets);
					if (convertedPart.type !== "file") throw new Error("Expected tool result file content to convert to file.");
					return convertedPart;
				}
				case "file-data":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"file-data\"",
						message: `The "file-data" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'data', data } instead.`
					});
					return {
						type: "file",
						data: {
							type: "data",
							data: item.data
						},
						filename: item.filename,
						mediaType: item.mediaType,
						providerOptions: item.providerOptions
					};
				case "file-url": {
					const mediaType = (_a23 = item.mediaType) != null ? _a23 : getMediaTypeFromUrl(item.url);
					let message = `The "file-url" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'url', url } instead.`;
					if (!item.mediaType) {
						const inferenceSuffix = mediaType === "application/octet-stream" ? `Unable to infer media type from URL. Defaulting to 'application/octet-stream'.` : `Inferred media type '${mediaType}' from URL.`;
						message = `The "file-url" tool result content part with URL "${item.url}" is missing a "mediaType". ${inferenceSuffix} ${message}`;
					}
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"file-url\"",
						message
					});
					return {
						type: "file",
						data: {
							type: "url",
							url: new URL(item.url)
						},
						mediaType,
						providerOptions: item.providerOptions
					};
				}
				case "file-id":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"file-id\"",
						message: `The "file-id" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'reference', reference } instead.`
					});
					return {
						type: "file",
						data: {
							type: "reference",
							reference: convertFileIdToProviderReference({
								fileId: item.fileId,
								provider
							})
						},
						mediaType: "application",
						providerOptions: item.providerOptions
					};
				case "file-reference":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"file-reference\"",
						message: `The "file-reference" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'reference', reference } instead.`
					});
					return {
						type: "file",
						data: {
							type: "reference",
							reference: item.providerReference
						},
						mediaType: "application",
						providerOptions: item.providerOptions
					};
				case "image-data":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"image-data\"",
						message: `The "image-data" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'data', data } instead.`
					});
					return {
						type: "file",
						data: {
							type: "data",
							data: item.data
						},
						mediaType: item.mediaType,
						providerOptions: item.providerOptions
					};
				case "image-url":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"image-url\"",
						message: `The "image-url" type for tool result content is deprecated. Use the "file" type with mediaType 'image' (or a specific image/* subtype) and { type: 'url', url } instead.`
					});
					return {
						type: "file",
						data: {
							type: "url",
							url: new URL(item.url)
						},
						mediaType: "image",
						providerOptions: item.providerOptions
					};
				case "image-file-id":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"image-file-id\"",
						message: `The "image-file-id" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'reference', reference } instead.`
					});
					return {
						type: "file",
						data: {
							type: "reference",
							reference: convertFileIdToProviderReference({
								fileId: item.fileId,
								provider
							})
						},
						mediaType: "image",
						providerOptions: item.providerOptions
					};
				case "image-file-reference":
					warnings.push({
						type: "deprecated",
						setting: "\"tool-result\" content of type \"image-file-reference\"",
						message: `The "image-file-reference" type for tool result content is deprecated. Use the "file" type with mediaType and { type: 'reference', reference } instead.`
					});
					return {
						type: "file",
						data: {
							type: "reference",
							reference: item.providerReference
						},
						mediaType: "image",
						providerOptions: item.providerOptions
					};
				default: return item;
			}
		})
	};
}
function convertFileIdToProviderReference({ fileId, provider }) {
	if (typeof fileId === "object") return fileId;
	if (provider == null) throw new Error("Cannot convert string fileId to provider reference without a provider ID. Use a Record<string, string> fileId or switch to the file-reference type.");
	return { [provider]: fileId };
}
var URL_EXTENSION_TO_MEDIA_TYPE = {
	jpg: "image/jpeg",
	jpeg: "image/jpeg",
	png: "image/png",
	gif: "image/gif",
	webp: "image/webp",
	svg: "image/svg+xml",
	avif: "image/avif",
	heic: "image/heic",
	bmp: "image/bmp",
	tiff: "image/tiff",
	tif: "image/tiff",
	pdf: "application/pdf",
	mp4: "video/mp4",
	webm: "video/webm",
	mp3: "audio/mpeg",
	wav: "audio/wav",
	ogg: "audio/ogg"
};
function getMediaTypeFromUrl(url, fallbackMediaType = "application/octet-stream") {
	var _a23;
	try {
		const fileExtension = (_a23 = new URL(url).pathname.split(".").pop()) == null ? void 0 : _a23.toLowerCase();
		if (fileExtension && Object.hasOwn(URL_EXTENSION_TO_MEDIA_TYPE, fileExtension)) return URL_EXTENSION_TO_MEDIA_TYPE[fileExtension];
	} catch (e) {}
	return fallbackMediaType;
}
function prepareToolChoice({ toolChoice }) {
	return toolChoice == null ? { type: "auto" } : typeof toolChoice === "string" ? { type: toolChoice } : {
		type: "tool",
		toolName: toolChoice.toolName
	};
}
function isNonEmptyObject(object3) {
	return object3 != null && Object.keys(object3).length > 0;
}
async function prepareTools({ tools, toolOrder, toolsContext = {}, experimental_sandbox: sandbox }) {
	if (!isNonEmptyObject(tools)) return;
	const languageModelTools = [];
	for (const [name23, tool2] of orderToolEntries({
		tools,
		toolOrder
	})) {
		const toolType = tool2.type;
		switch (toolType) {
			case void 0:
			case "dynamic":
			case "function": {
				const description = resolveToolDescription({
					tool: tool2,
					toolName: name23,
					toolsContext,
					experimental_sandbox: sandbox
				});
				const providerOptions = tool2.providerOptions;
				const inputExamples = tool2.inputExamples;
				const strict = tool2.strict;
				languageModelTools.push({
					type: "function",
					name: name23,
					inputSchema: await asSchema(tool2.inputSchema).jsonSchema,
					...description != null ? { description } : {},
					...inputExamples != null ? { inputExamples } : {},
					...providerOptions != null ? { providerOptions } : {},
					...strict != null ? { strict } : {}
				});
				break;
			}
			case "provider":
				languageModelTools.push({
					type: "provider",
					name: name23,
					id: tool2.id,
					args: tool2.args
				});
				break;
			default: throw new Error(`Unsupported tool type: ${toolType}`);
		}
	}
	return languageModelTools;
}
function orderToolEntries({ tools, toolOrder }) {
	if (toolOrder == null) return Object.entries(tools);
	const toolEntries = Object.entries(tools);
	const orderedTools = toolEntries.filter(([name23]) => toolOrder.includes(name23)).sort(([nameA], [nameB]) => toolOrder.indexOf(nameA) - toolOrder.indexOf(nameB));
	const unorderedTools = toolEntries.filter(([name23]) => !toolOrder.includes(name23)).sort(([nameA], [nameB]) => nameA < nameB ? -1 : nameA > nameB ? 1 : 0);
	return [...orderedTools, ...unorderedTools];
}
function resolveToolDescription({ tool: tool2, toolName, toolsContext, experimental_sandbox: sandbox }) {
	return tool2.description === void 0 ? void 0 : typeof tool2.description === "string" ? tool2.description : tool2.description({
		context: toolsContext[toolName],
		experimental_sandbox: sandbox
	});
}
var z = {
	array,
	boolean,
	custom,
	discriminatedUnion,
	enum: _enum,
	instanceof: _instanceof,
	lazy,
	literal,
	looseObject,
	never,
	null: _null,
	number,
	object,
	record,
	string,
	union,
	unknown
};
var jsonValueSchema = z.lazy(() => z.union([
	z.null(),
	z.string(),
	z.number(),
	z.boolean(),
	z.record(z.string(), jsonValueSchema.optional()),
	z.array(jsonValueSchema)
]));
var providerMetadataSchema = z.record(z.string(), z.record(z.string(), jsonValueSchema.optional()));
var fileInlineDataSchema = z.union([
	z.string(),
	z.instanceof(Uint8Array),
	z.instanceof(ArrayBuffer),
	z.custom(isBuffer, { message: "Must be a Buffer" })
]);
var providerReferenceSchema = z.record(z.string(), z.string());
var textPartSchema = z.object({
	type: z.literal("text"),
	text: z.string(),
	providerOptions: providerMetadataSchema.optional()
});
var imagePartSchema = z.object({
	type: z.literal("image"),
	image: z.union([
		fileInlineDataSchema,
		z.instanceof(URL),
		providerReferenceSchema
	]),
	mediaType: z.string().optional(),
	providerOptions: providerMetadataSchema.optional()
});
var taggedFileDataSchema = z.discriminatedUnion("type", [
	z.object({
		type: z.literal("data"),
		data: fileInlineDataSchema
	}),
	z.object({
		type: z.literal("url"),
		url: z.instanceof(URL)
	}),
	z.object({
		type: z.literal("reference"),
		reference: providerReferenceSchema
	}),
	z.object({
		type: z.literal("text"),
		text: z.string()
	})
]);
var taggedReasoningFileDataSchema = z.discriminatedUnion("type", [z.object({
	type: z.literal("data"),
	data: fileInlineDataSchema
}), z.object({
	type: z.literal("url"),
	url: z.instanceof(URL)
})]);
var filePartSchema = z.object({
	type: z.literal("file"),
	data: z.union([
		taggedFileDataSchema,
		fileInlineDataSchema,
		z.instanceof(URL),
		providerReferenceSchema
	]),
	filename: z.string().optional(),
	mediaType: z.string(),
	providerOptions: providerMetadataSchema.optional()
});
var reasoningPartSchema = z.object({
	type: z.literal("reasoning"),
	text: z.string(),
	providerOptions: providerMetadataSchema.optional()
});
var customPartSchema = z.object({
	type: z.literal("custom"),
	kind: z.string().transform((value) => value),
	providerOptions: providerMetadataSchema.optional()
});
var reasoningFilePartSchema = z.object({
	type: z.literal("reasoning-file"),
	data: z.union([
		taggedReasoningFileDataSchema,
		fileInlineDataSchema,
		z.instanceof(URL)
	]),
	mediaType: z.string(),
	providerOptions: providerMetadataSchema.optional()
});
var toolCallPartSchema = z.object({
	type: z.literal("tool-call"),
	toolCallId: z.string(),
	toolName: z.string(),
	input: z.unknown(),
	providerOptions: providerMetadataSchema.optional(),
	providerExecuted: z.boolean().optional()
});
var outputSchema = z.discriminatedUnion("type", [
	z.object({
		type: z.literal("text"),
		value: z.string(),
		providerOptions: providerMetadataSchema.optional()
	}),
	z.object({
		type: z.literal("json"),
		value: jsonValueSchema,
		providerOptions: providerMetadataSchema.optional()
	}),
	z.object({
		type: z.literal("execution-denied"),
		reason: z.string().optional(),
		providerOptions: providerMetadataSchema.optional()
	}),
	z.object({
		type: z.literal("error-text"),
		value: z.string(),
		providerOptions: providerMetadataSchema.optional()
	}),
	z.object({
		type: z.literal("error-json"),
		value: jsonValueSchema,
		providerOptions: providerMetadataSchema.optional()
	}),
	z.object({
		type: z.literal("content"),
		value: z.array(z.union([
			z.object({
				type: z.literal("text"),
				text: z.string(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("file"),
				data: taggedFileDataSchema,
				mediaType: z.string(),
				filename: z.string().optional(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("file-data"),
				data: z.string(),
				mediaType: z.string(),
				filename: z.string().optional(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("file-url"),
				url: z.string(),
				mediaType: z.string().optional(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("file-id"),
				fileId: z.union([z.string(), z.record(z.string(), z.string())]),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("file-reference"),
				providerReference: z.record(z.string(), z.string()),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("image-data"),
				data: z.string(),
				mediaType: z.string(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("image-url"),
				url: z.string(),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("image-file-id"),
				fileId: z.union([z.string(), z.record(z.string(), z.string())]),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("image-file-reference"),
				providerReference: z.record(z.string(), z.string()),
				providerOptions: providerMetadataSchema.optional()
			}),
			z.object({
				type: z.literal("custom"),
				providerOptions: providerMetadataSchema.optional()
			})
		]))
	})
]);
var toolResultPartSchema = z.object({
	type: z.literal("tool-result"),
	toolCallId: z.string(),
	toolName: z.string(),
	output: outputSchema,
	providerOptions: providerMetadataSchema.optional()
});
var toolApprovalRequestSchema = z.object({
	type: z.literal("tool-approval-request"),
	approvalId: z.string(),
	toolCallId: z.string()
});
var toolApprovalResponseSchema = z.object({
	type: z.literal("tool-approval-response"),
	approvalId: z.string(),
	approved: z.boolean(),
	reason: z.string().optional()
});
var systemModelMessageSchema = z.object({
	role: z.literal("system"),
	content: z.string(),
	providerOptions: providerMetadataSchema.optional()
});
var userModelMessageSchema = z.object({
	role: z.literal("user"),
	content: z.union([z.string(), z.array(z.union([
		textPartSchema,
		imagePartSchema,
		filePartSchema
	]))]),
	providerOptions: providerMetadataSchema.optional()
});
var assistantModelMessageSchema = z.object({
	role: z.literal("assistant"),
	content: z.union([z.string(), z.array(z.union([
		textPartSchema,
		customPartSchema,
		filePartSchema,
		reasoningPartSchema,
		reasoningFilePartSchema,
		toolCallPartSchema,
		toolResultPartSchema,
		toolApprovalRequestSchema
	]))]),
	providerOptions: providerMetadataSchema.optional()
});
var toolModelMessageSchema = z.object({
	role: z.literal("tool"),
	content: z.array(z.union([toolResultPartSchema, toolApprovalResponseSchema])),
	providerOptions: providerMetadataSchema.optional()
});
var modelMessageSchema = z.union([
	systemModelMessageSchema,
	userModelMessageSchema,
	assistantModelMessageSchema,
	toolModelMessageSchema
]);
async function standardizePrompt({ allowSystemInMessages = false, system, instructions = system, prompt, messages }) {
	if (prompt == null && messages == null) throw new InvalidPromptError({
		prompt,
		message: "prompt or messages must be defined"
	});
	if (prompt != null && messages != null) throw new InvalidPromptError({
		prompt,
		message: "prompt and messages cannot be defined at the same time"
	});
	if (typeof instructions !== "string" && !asArray(instructions).every((message) => message.role === "system")) throw new InvalidPromptError({
		prompt,
		message: "instructions must be a string, SystemModelMessage, or array of SystemModelMessage"
	});
	if (prompt != null && typeof prompt === "string") messages = [{
		role: "user",
		content: prompt
	}];
	else if (prompt != null && Array.isArray(prompt)) messages = prompt;
	else if (messages == null) throw new InvalidPromptError({
		prompt,
		message: "prompt or messages must be defined"
	});
	if (messages.length === 0) throw new InvalidPromptError({
		prompt,
		message: "messages must not be empty"
	});
	if (!allowSystemInMessages && messages.some((message) => message.role === "system")) throw new InvalidPromptError({
		prompt,
		message: "System messages are not allowed in the prompt or messages fields. Use the instructions option instead."
	});
	const validationResult = await safeValidateTypes({
		value: messages,
		schema: z.array(modelMessageSchema)
	});
	if (!validationResult.success) throw new InvalidPromptError({
		prompt,
		message: "The messages do not match the ModelMessage[] schema.",
		cause: validationResult.error
	});
	return {
		messages,
		instructions
	};
}
function asLanguageModelUsage(usage) {
	return {
		inputTokens: usage.inputTokens.total,
		inputTokenDetails: {
			noCacheTokens: usage.inputTokens.noCache,
			cacheReadTokens: usage.inputTokens.cacheRead,
			cacheWriteTokens: usage.inputTokens.cacheWrite
		},
		outputTokens: usage.outputTokens.total,
		outputTokenDetails: {
			textTokens: usage.outputTokens.text,
			reasoningTokens: usage.outputTokens.reasoning
		},
		totalTokens: addTokenCounts(usage.inputTokens.total, usage.outputTokens.total),
		raw: usage.raw
	};
}
function addTokenCounts(tokenCount1, tokenCount2) {
	return tokenCount1 == null && tokenCount2 == null ? void 0 : (tokenCount1 != null ? tokenCount1 : 0) + (tokenCount2 != null ? tokenCount2 : 0);
}
function getOwn(obj, key) {
	return obj != null && Object.hasOwn(obj, key) ? obj[key] : void 0;
}
function now() {
	var _a23, _b;
	return (_b = (_a23 = globalThis == null ? void 0 : globalThis.performance) == null ? void 0 : _a23.now()) != null ? _b : Date.now();
}
async function notify(options) {
	await Promise.all(asArray(options.callbacks).map(async (callback) => {
		try {
			await (callback == null ? void 0 : callback(options.event));
		} catch (e) {}
	}));
}
function calculateTokensPerSecond({ tokens, durationMs }) {
	const tokenRate = 1e3 * (tokens != null ? tokens : 0) / (durationMs != null ? durationMs : 0);
	return Number.isFinite(tokenRate) ? tokenRate : 0;
}
var DefaultGeneratedFile = class {
	constructor({ data, mediaType }) {
		const isUint8Array = data instanceof Uint8Array;
		this.base64Data = isUint8Array ? void 0 : data;
		this.uint8ArrayData = isUint8Array ? data : void 0;
		this.mediaType = mediaType;
	}
	get base64() {
		if (this.base64Data == null) this.base64Data = convertUint8ArrayToBase64(this.uint8ArrayData);
		return this.base64Data;
	}
	get uint8Array() {
		if (this.uint8ArrayData == null) this.uint8ArrayData = convertBase64ToUint8Array(this.base64Data);
		return this.uint8ArrayData;
	}
};
var DefaultGeneratedFileWithType = class extends DefaultGeneratedFile {
	constructor(options) {
		super(options);
		this.type = "file";
	}
};
__export({}, {
	array: () => array2,
	choice: () => choice,
	json: () => json,
	object: () => object2,
	text: () => text
});
function fixJson(input) {
	const stack = ["ROOT"];
	let lastValidIndex = -1;
	let literalStart = null;
	let unicodeEscapeDigits = 0;
	function isHexDigit(char) {
		return char >= "0" && char <= "9" || char >= "A" && char <= "F" || char >= "a" && char <= "f";
	}
	function processValueStart(char, i, swapState) {
		switch (char) {
			case "\"":
				lastValidIndex = i;
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_STRING");
				break;
			case "f":
			case "t":
			case "n":
				lastValidIndex = i;
				literalStart = i;
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_LITERAL");
				break;
			case "-":
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_NUMBER");
				break;
			case "0":
			case "1":
			case "2":
			case "3":
			case "4":
			case "5":
			case "6":
			case "7":
			case "8":
			case "9":
				lastValidIndex = i;
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_NUMBER");
				break;
			case "{":
				lastValidIndex = i;
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_OBJECT_START");
				break;
			case "[":
				lastValidIndex = i;
				stack.pop();
				stack.push(swapState);
				stack.push("INSIDE_ARRAY_START");
		}
	}
	function processAfterObjectValue(char, i) {
		switch (char) {
			case ",":
				stack.pop();
				stack.push("INSIDE_OBJECT_AFTER_COMMA");
				break;
			case "}":
				lastValidIndex = i;
				stack.pop();
		}
	}
	function processAfterArrayValue(char, i) {
		switch (char) {
			case ",":
				stack.pop();
				stack.push("INSIDE_ARRAY_AFTER_COMMA");
				break;
			case "]":
				lastValidIndex = i;
				stack.pop();
		}
	}
	for (let i = 0; i < input.length; i++) {
		const char = input[i];
		switch (stack[stack.length - 1]) {
			case "ROOT":
				processValueStart(char, i, "FINISH");
				break;
			case "INSIDE_OBJECT_START":
				switch (char) {
					case "\"":
						stack.pop();
						stack.push("INSIDE_OBJECT_KEY");
						break;
					case "}":
						lastValidIndex = i;
						stack.pop();
				}
				break;
			case "INSIDE_OBJECT_AFTER_COMMA":
				switch (char) {
					case "\"":
						stack.pop();
						stack.push("INSIDE_OBJECT_KEY");
				}
				break;
			case "INSIDE_OBJECT_KEY":
				switch (char) {
					case "\"":
						stack.pop();
						stack.push("INSIDE_OBJECT_AFTER_KEY");
				}
				break;
			case "INSIDE_OBJECT_AFTER_KEY":
				switch (char) {
					case ":":
						stack.pop();
						stack.push("INSIDE_OBJECT_BEFORE_VALUE");
				}
				break;
			case "INSIDE_OBJECT_BEFORE_VALUE":
				processValueStart(char, i, "INSIDE_OBJECT_AFTER_VALUE");
				break;
			case "INSIDE_OBJECT_AFTER_VALUE":
				processAfterObjectValue(char, i);
				break;
			case "INSIDE_STRING":
				switch (char) {
					case "\"":
						stack.pop();
						lastValidIndex = i;
						break;
					case "\\":
						stack.push("INSIDE_STRING_ESCAPE");
						break;
					default: lastValidIndex = i;
				}
				break;
			case "INSIDE_ARRAY_START":
				switch (char) {
					case "]":
						lastValidIndex = i;
						stack.pop();
						break;
					default:
						lastValidIndex = i;
						processValueStart(char, i, "INSIDE_ARRAY_AFTER_VALUE");
				}
				break;
			case "INSIDE_ARRAY_AFTER_VALUE":
				switch (char) {
					case ",":
						stack.pop();
						stack.push("INSIDE_ARRAY_AFTER_COMMA");
						break;
					case "]":
						lastValidIndex = i;
						stack.pop();
						break;
					default: lastValidIndex = i;
				}
				break;
			case "INSIDE_ARRAY_AFTER_COMMA":
				processValueStart(char, i, "INSIDE_ARRAY_AFTER_VALUE");
				break;
			case "INSIDE_STRING_ESCAPE":
				stack.pop();
				if (char === "u") {
					unicodeEscapeDigits = 0;
					stack.push("INSIDE_STRING_UNICODE_ESCAPE");
				} else lastValidIndex = i;
				break;
			case "INSIDE_STRING_UNICODE_ESCAPE":
				if (isHexDigit(char)) {
					unicodeEscapeDigits++;
					if (unicodeEscapeDigits === 4) {
						stack.pop();
						lastValidIndex = i;
					}
				}
				break;
			case "INSIDE_NUMBER":
				switch (char) {
					case "0":
					case "1":
					case "2":
					case "3":
					case "4":
					case "5":
					case "6":
					case "7":
					case "8":
					case "9":
						lastValidIndex = i;
						break;
					case "e":
					case "E":
					case "-":
					case ".": break;
					case ",":
						stack.pop();
						if (stack[stack.length - 1] === "INSIDE_ARRAY_AFTER_VALUE") processAfterArrayValue(char, i);
						if (stack[stack.length - 1] === "INSIDE_OBJECT_AFTER_VALUE") processAfterObjectValue(char, i);
						break;
					case "}":
						stack.pop();
						if (stack[stack.length - 1] === "INSIDE_OBJECT_AFTER_VALUE") processAfterObjectValue(char, i);
						break;
					case "]":
						stack.pop();
						if (stack[stack.length - 1] === "INSIDE_ARRAY_AFTER_VALUE") processAfterArrayValue(char, i);
						break;
					default: stack.pop();
				}
				break;
			case "INSIDE_LITERAL": {
				const partialLiteral = input.substring(literalStart, i + 1);
				if (!"false".startsWith(partialLiteral) && !"true".startsWith(partialLiteral) && !"null".startsWith(partialLiteral)) {
					stack.pop();
					if (stack[stack.length - 1] === "INSIDE_OBJECT_AFTER_VALUE") processAfterObjectValue(char, i);
					else if (stack[stack.length - 1] === "INSIDE_ARRAY_AFTER_VALUE") processAfterArrayValue(char, i);
				} else lastValidIndex = i;
				break;
			}
		}
	}
	let result = input.slice(0, lastValidIndex + 1);
	for (let i = stack.length - 1; i >= 0; i--) switch (stack[i]) {
		case "INSIDE_STRING":
			result += "\"";
			break;
		case "INSIDE_OBJECT_KEY":
		case "INSIDE_OBJECT_AFTER_KEY":
		case "INSIDE_OBJECT_AFTER_COMMA":
		case "INSIDE_OBJECT_START":
		case "INSIDE_OBJECT_BEFORE_VALUE":
		case "INSIDE_OBJECT_AFTER_VALUE":
			result += "}";
			break;
		case "INSIDE_ARRAY_START":
		case "INSIDE_ARRAY_AFTER_COMMA":
		case "INSIDE_ARRAY_AFTER_VALUE":
			result += "]";
			break;
		case "INSIDE_LITERAL": {
			const partialLiteral = input.substring(literalStart, input.length);
			if ("true".startsWith(partialLiteral)) result += "true".slice(partialLiteral.length);
			else if ("false".startsWith(partialLiteral)) result += "false".slice(partialLiteral.length);
			else if ("null".startsWith(partialLiteral)) result += "null".slice(partialLiteral.length);
		}
	}
	return result;
}
async function parsePartialJson(jsonText) {
	if (jsonText === void 0) return {
		value: void 0,
		state: "undefined-input"
	};
	let result = await safeParseJSON({ text: jsonText });
	if (result.success) return {
		value: result.value,
		state: "successful-parse"
	};
	result = await safeParseJSON({ text: fixJson(jsonText) });
	if (result.success) return {
		value: result.value,
		state: "repaired-parse"
	};
	return {
		value: void 0,
		state: "failed-parse"
	};
}
var text = () => ({
	name: "text",
	responseFormat: Promise.resolve({ type: "text" }),
	async parseCompleteOutput({ text: text2 }) {
		return text2;
	},
	async parsePartialOutput({ text: text2 }) {
		return { partial: text2 };
	},
	createElementStreamTransform() {}
});
var object2 = ({ schema: inputSchema, name: name23, description }) => {
	const schema = asSchema(inputSchema);
	return {
		name: "object",
		responseFormat: resolve(schema.jsonSchema).then((jsonSchema2) => ({
			type: "json",
			schema: jsonSchema2,
			...name23 != null && { name: name23 },
			...description != null && { description }
		})),
		async parseCompleteOutput({ text: text2 }, context) {
			const parseResult = await safeParseJSON({ text: text2 });
			if (!parseResult.success) throw new NoObjectGeneratedError({
				message: "No object generated: could not parse the response.",
				cause: parseResult.error,
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			const validationResult = await safeValidateTypes({
				value: parseResult.value,
				schema
			});
			if (!validationResult.success) throw new NoObjectGeneratedError({
				message: "No object generated: response did not match schema.",
				cause: validationResult.error,
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			return validationResult.value;
		},
		async parsePartialOutput({ text: text2 }) {
			const result = await parsePartialJson(text2);
			switch (result.state) {
				case "failed-parse":
				case "undefined-input": return;
				case "repaired-parse":
				case "successful-parse": return { partial: result.value };
			}
		},
		createElementStreamTransform() {}
	};
};
var array2 = ({ element: inputElementSchema, name: name23, description }) => {
	const elementSchema = asSchema(inputElementSchema);
	return {
		name: "array",
		responseFormat: resolve(elementSchema.jsonSchema).then((jsonSchema2) => {
			const { $schema: _$schema, definitions, $defs, ...itemSchema } = jsonSchema2;
			return {
				type: "json",
				schema: {
					$schema: "http://json-schema.org/draft-07/schema#",
					...definitions != null && { definitions },
					...$defs != null && { $defs },
					type: "object",
					properties: { elements: {
						type: "array",
						items: itemSchema
					} },
					required: ["elements"],
					additionalProperties: false
				},
				...name23 != null && { name: name23 },
				...description != null && { description }
			};
		}),
		async parseCompleteOutput({ text: text2 }, context) {
			const parseResult = await safeParseJSON({ text: text2 });
			if (!parseResult.success) throw new NoObjectGeneratedError({
				message: "No object generated: could not parse the response.",
				cause: parseResult.error,
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			const outerValue = parseResult.value;
			if (outerValue == null || typeof outerValue !== "object" || !("elements" in outerValue) || !Array.isArray(outerValue.elements)) throw new NoObjectGeneratedError({
				message: "No object generated: response did not match schema.",
				cause: new TypeValidationError({
					value: outerValue,
					cause: "response must be an object with an elements array"
				}),
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			const validatedElements = [];
			for (const element of outerValue.elements) {
				const validationResult = await safeValidateTypes({
					value: element,
					schema: elementSchema
				});
				if (!validationResult.success) throw new NoObjectGeneratedError({
					message: "No object generated: response did not match schema.",
					cause: validationResult.error,
					text: text2,
					response: context.response,
					usage: context.usage,
					finishReason: context.finishReason
				});
				validatedElements.push(validationResult.value);
			}
			return validatedElements;
		},
		async parsePartialOutput({ text: text2 }) {
			const result = await parsePartialJson(text2);
			switch (result.state) {
				case "failed-parse":
				case "undefined-input": return;
				case "repaired-parse":
				case "successful-parse": {
					const outerValue = result.value;
					if (outerValue == null || typeof outerValue !== "object" || !("elements" in outerValue) || !Array.isArray(outerValue.elements)) return;
					const rawElements = result.state === "repaired-parse" && outerValue.elements.length > 0 ? outerValue.elements.slice(0, -1) : outerValue.elements;
					const parsedElements = [];
					for (const rawElement of rawElements) {
						const validationResult = await safeValidateTypes({
							value: rawElement,
							schema: elementSchema
						});
						if (validationResult.success) parsedElements.push(validationResult.value);
					}
					return { partial: parsedElements };
				}
			}
		},
		createElementStreamTransform() {
			let publishedElements = 0;
			return new TransformStream({ transform({ partialOutput }, controller) {
				if (partialOutput != null) for (; publishedElements < partialOutput.length; publishedElements++) controller.enqueue(partialOutput[publishedElements]);
			} });
		}
	};
};
var choice = ({ options: choiceOptions, name: name23, description }) => {
	return {
		name: "choice",
		responseFormat: Promise.resolve({
			type: "json",
			schema: {
				$schema: "http://json-schema.org/draft-07/schema#",
				type: "object",
				properties: { result: {
					type: "string",
					enum: choiceOptions
				} },
				required: ["result"],
				additionalProperties: false
			},
			...name23 != null && { name: name23 },
			...description != null && { description }
		}),
		async parseCompleteOutput({ text: text2 }, context) {
			const parseResult = await safeParseJSON({ text: text2 });
			if (!parseResult.success) throw new NoObjectGeneratedError({
				message: "No object generated: could not parse the response.",
				cause: parseResult.error,
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			const outerValue = parseResult.value;
			if (outerValue == null || typeof outerValue !== "object" || !("result" in outerValue) || typeof outerValue.result !== "string" || !choiceOptions.includes(outerValue.result)) throw new NoObjectGeneratedError({
				message: "No object generated: response did not match schema.",
				cause: new TypeValidationError({
					value: outerValue,
					cause: "response must be an object that contains a choice value."
				}),
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			return outerValue.result;
		},
		async parsePartialOutput({ text: text2 }) {
			const result = await parsePartialJson(text2);
			switch (result.state) {
				case "failed-parse":
				case "undefined-input": return;
				case "repaired-parse":
				case "successful-parse": {
					const outerValue = result.value;
					if (outerValue == null || typeof outerValue !== "object" || !("result" in outerValue) || typeof outerValue.result !== "string") return;
					const potentialMatches = choiceOptions.filter((choiceOption) => choiceOption.startsWith(outerValue.result));
					if (result.state === "successful-parse") return potentialMatches.includes(outerValue.result) ? { partial: outerValue.result } : void 0;
					else return potentialMatches.length === 1 ? { partial: potentialMatches[0] } : void 0;
				}
			}
		},
		createElementStreamTransform() {}
	};
};
var json = ({ name: name23, description } = {}) => {
	return {
		name: "json",
		responseFormat: Promise.resolve({
			type: "json",
			...name23 != null && { name: name23 },
			...description != null && { description }
		}),
		async parseCompleteOutput({ text: text2 }, context) {
			const parseResult = await safeParseJSON({ text: text2 });
			if (!parseResult.success) throw new NoObjectGeneratedError({
				message: "No object generated: could not parse the response.",
				cause: parseResult.error,
				text: text2,
				response: context.response,
				usage: context.usage,
				finishReason: context.finishReason
			});
			return parseResult.value;
		},
		async parsePartialOutput({ text: text2 }) {
			const result = await parsePartialJson(text2);
			switch (result.state) {
				case "failed-parse":
				case "undefined-input": return;
				case "repaired-parse":
				case "successful-parse": return result.value === void 0 ? void 0 : { partial: result.value };
			}
		},
		createElementStreamTransform() {}
	};
};
async function parseToolCall({ toolCall, tools, repairToolCall, refineToolInput, messages, instructions }) {
	try {
		if (tools == null) {
			if (toolCall.providerExecuted && toolCall.dynamic) return await refineParsedToolCallInput({
				toolCall: await parseProviderExecutedDynamicToolCall(toolCall),
				refineToolInput
			});
			throw new NoSuchToolError({ toolName: toolCall.toolName });
		}
		try {
			return await refineParsedToolCallInput({
				toolCall: await doParseToolCall({
					toolCall,
					tools
				}),
				refineToolInput
			});
		} catch (error) {
			if (repairToolCall == null || !(NoSuchToolError.isInstance(error) || InvalidToolInputError.isInstance(error))) throw error;
			let repairedToolCall = null;
			try {
				repairedToolCall = await repairToolCall({
					toolCall,
					tools,
					inputSchema: async ({ toolName }) => {
						var _a23;
						const inputSchema = (_a23 = getOwn(tools, toolName)) == null ? void 0 : _a23.inputSchema;
						return await asSchema(inputSchema).jsonSchema;
					},
					instructions,
					system: instructions,
					messages,
					error
				});
			} catch (repairError) {
				throw new ToolCallRepairError({
					cause: repairError,
					originalError: error
				});
			}
			if (repairedToolCall == null) throw error;
			return await refineParsedToolCallInput({
				toolCall: await doParseToolCall({
					toolCall: repairedToolCall,
					tools
				}),
				refineToolInput
			});
		}
	} catch (error) {
		const parsedInput = await safeParseJSON({ text: toolCall.input });
		const input = parsedInput.success ? parsedInput.value : toolCall.input;
		const tool2 = getOwn(tools, toolCall.toolName);
		return {
			type: "tool-call",
			toolCallId: toolCall.toolCallId,
			toolName: toolCall.toolName,
			input,
			dynamic: true,
			invalid: true,
			error,
			title: tool2 == null ? void 0 : tool2.title,
			providerExecuted: toolCall.providerExecuted,
			providerMetadata: toolCall.providerMetadata,
			...(tool2 == null ? void 0 : tool2.metadata) != null ? { toolMetadata: tool2.metadata } : {}
		};
	}
}
async function refineParsedToolCallInput({ toolCall, refineToolInput }) {
	const refine = getOwn(refineToolInput, toolCall.toolName);
	if (refine == null) return toolCall;
	return {
		...toolCall,
		input: await refine(toolCall.input)
	};
}
async function parseProviderExecutedDynamicToolCall(toolCall) {
	const parseResult = toolCall.input.trim() === "" ? {
		success: true,
		value: {}
	} : await safeParseJSON({ text: toolCall.input });
	if (parseResult.success === false) throw new InvalidToolInputError({
		toolName: toolCall.toolName,
		toolInput: toolCall.input,
		cause: parseResult.error
	});
	return {
		type: "tool-call",
		toolCallId: toolCall.toolCallId,
		toolName: toolCall.toolName,
		input: parseResult.value,
		providerExecuted: true,
		dynamic: true,
		providerMetadata: toolCall.providerMetadata
	};
}
async function doParseToolCall({ toolCall, tools }) {
	const toolName = toolCall.toolName;
	const tool2 = getOwn(tools, toolName);
	if (tool2 == null) {
		if (toolCall.providerExecuted && toolCall.dynamic) return await parseProviderExecutedDynamicToolCall(toolCall);
		throw new NoSuchToolError({
			toolName: toolCall.toolName,
			availableTools: Object.keys(tools)
		});
	}
	const schema = asSchema(tool2.inputSchema);
	const parseResult = toolCall.input.trim() === "" ? await safeValidateTypes({
		value: {},
		schema
	}) : await safeParseJSON({
		text: toolCall.input,
		schema
	});
	if (parseResult.success === false) throw new InvalidToolInputError({
		toolName,
		toolInput: toolCall.input,
		cause: parseResult.error
	});
	return tool2.type === "dynamic" ? {
		type: "tool-call",
		toolCallId: toolCall.toolCallId,
		toolName: toolCall.toolName,
		input: parseResult.value,
		providerExecuted: toolCall.providerExecuted,
		providerMetadata: toolCall.providerMetadata,
		...tool2.metadata != null ? { toolMetadata: tool2.metadata } : {},
		dynamic: true,
		title: tool2.title
	} : {
		type: "tool-call",
		toolCallId: toolCall.toolCallId,
		toolName,
		input: parseResult.value,
		providerExecuted: toolCall.providerExecuted,
		providerMetadata: toolCall.providerMetadata,
		...tool2.metadata != null ? { toolMetadata: tool2.metadata } : {},
		title: tool2.title
	};
}
function sumTokenCounts(tokenCount1, tokenCount2) {
	return tokenCount1 == null && tokenCount2 == null ? void 0 : (tokenCount1 != null ? tokenCount1 : 0) + (tokenCount2 != null ? tokenCount2 : 0);
}
new TextEncoder();
new TextEncoder();
createIdGenerator({
	prefix: "aitxt",
	size: 24
});
createIdGenerator({
	prefix: "call",
	size: 24
});
TransformStream;
var toolMetadataSchema = z.record(z.string(), jsonValueSchema.optional());
lazySchema(() => zodSchema(z.union([
	z.looseObject({
		type: z.literal("text-start"),
		id: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("text-delta"),
		id: z.string(),
		delta: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("text-end"),
		id: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("error"),
		errorText: z.string()
	}),
	z.looseObject({
		type: z.literal("tool-input-start"),
		toolCallId: z.string(),
		toolName: z.string(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional(),
		toolMetadata: toolMetadataSchema.optional(),
		dynamic: z.boolean().optional(),
		title: z.string().optional()
	}),
	z.looseObject({
		type: z.literal("tool-input-delta"),
		toolCallId: z.string(),
		inputTextDelta: z.string()
	}),
	z.looseObject({
		type: z.literal("tool-input-available"),
		toolCallId: z.string(),
		toolName: z.string(),
		input: z.unknown(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional(),
		toolMetadata: toolMetadataSchema.optional(),
		dynamic: z.boolean().optional(),
		title: z.string().optional()
	}),
	z.looseObject({
		type: z.literal("tool-input-error"),
		toolCallId: z.string(),
		toolName: z.string(),
		input: z.unknown(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional(),
		toolMetadata: toolMetadataSchema.optional(),
		dynamic: z.boolean().optional(),
		errorText: z.string(),
		title: z.string().optional()
	}),
	z.looseObject({
		type: z.literal("tool-approval-request"),
		approvalId: z.string(),
		toolCallId: z.string(),
		isAutomatic: z.boolean().optional(),
		signature: z.string().optional()
	}),
	z.looseObject({
		type: z.literal("tool-approval-response"),
		approvalId: z.string(),
		approved: z.boolean(),
		reason: z.string().optional(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("tool-output-available"),
		toolCallId: z.string(),
		output: z.unknown(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional(),
		toolMetadata: toolMetadataSchema.optional(),
		dynamic: z.boolean().optional(),
		preliminary: z.boolean().optional()
	}),
	z.looseObject({
		type: z.literal("tool-output-error"),
		toolCallId: z.string(),
		errorText: z.string(),
		providerExecuted: z.boolean().optional(),
		providerMetadata: providerMetadataSchema.optional(),
		toolMetadata: toolMetadataSchema.optional(),
		dynamic: z.boolean().optional()
	}),
	z.looseObject({
		type: z.literal("tool-output-denied"),
		toolCallId: z.string()
	}),
	z.looseObject({
		type: z.literal("reasoning-start"),
		id: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("reasoning-delta"),
		id: z.string(),
		delta: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("reasoning-end"),
		id: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("custom"),
		kind: z.string().transform((value) => value),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("source-url"),
		sourceId: z.string(),
		url: z.string(),
		title: z.string().optional(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("source-document"),
		sourceId: z.string(),
		mediaType: z.string(),
		title: z.string(),
		filename: z.string().optional(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("file"),
		url: z.string(),
		mediaType: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.literal("reasoning-file"),
		url: z.string(),
		mediaType: z.string(),
		providerMetadata: providerMetadataSchema.optional()
	}),
	z.looseObject({
		type: z.custom((value) => typeof value === "string" && value.startsWith("data-"), { message: "Type must start with \"data-\"" }),
		id: z.string().optional(),
		data: z.unknown(),
		transient: z.boolean().optional()
	}),
	z.looseObject({ type: z.literal("start-step") }),
	z.looseObject({ type: z.literal("finish-step") }),
	z.looseObject({
		type: z.literal("start"),
		messageId: z.string().optional(),
		messageMetadata: z.unknown().optional()
	}),
	z.looseObject({
		type: z.literal("finish"),
		finishReason: z.enum([
			"stop",
			"length",
			"content-filter",
			"tool-calls",
			"error",
			"other"
		]).optional(),
		messageMetadata: z.unknown().optional()
	}),
	z.looseObject({
		type: z.literal("abort"),
		reason: z.string().optional()
	}),
	z.looseObject({
		type: z.literal("message-metadata"),
		messageMetadata: z.unknown()
	})
])));
function createAsyncIterableStream(source) {
	return asAsyncIterableStream(source.pipeThrough(new TransformStream()));
}
function asAsyncIterableStream(stream) {
	stream[Symbol.asyncIterator] = function() {
		const reader = this.getReader();
		let finished = false;
		async function cleanup(cancelStream) {
			var _a23;
			if (finished) return;
			finished = true;
			try {
				if (cancelStream) await ((_a23 = reader.cancel) == null ? void 0 : _a23.call(reader));
			} finally {
				try {
					reader.releaseLock();
				} catch (e) {}
			}
		}
		return {
			/**
			* Reads the next chunk from the stream.
			* @returns A promise resolving to the next IteratorResult.
			*/
			async next() {
				if (finished) return {
					done: true,
					value: void 0
				};
				let result;
				try {
					result = await reader.read();
				} catch (error) {
					await cleanup(false);
					throw error;
				}
				const { done, value } = result;
				if (done) {
					await cleanup(true);
					return {
						done: true,
						value: void 0
					};
				}
				return {
					done: false,
					value
				};
			},
			/**
			* May be called on early exit (e.g., break from for-await) or after completion.
			* Ensures the stream is cancelled and resources are released.
			* @returns A promise resolving to a completed IteratorResult.
			*/
			async return() {
				await cleanup(true);
				return {
					done: true,
					value: void 0
				};
			},
			/**
			* Called on early exit with error.
			* Ensures the stream is cancelled and resources are released, then rethrows the error.
			* @param err The error to throw.
			* @returns A promise that rejects with the provided error.
			*/
			async throw(err) {
				await cleanup(true);
				throw err;
			}
		};
	};
	return stream;
}
var originalGenerateId2 = createIdGenerator({
	prefix: "aitxt",
	size: 24
});
var originalGenerateCallId2 = createIdGenerator({
	prefix: "call",
	size: 24
});
async function streamLanguageModelCall({ model, tools, toolOrder, output, toolChoice, prompt, system, instructions, messages, allowSystemInMessages, download: download2, abortSignal, headers, includeRawChunks, providerOptions, repairToolCall, refineToolInput, executeLanguageModelCallInTelemetryContext = async ({ execute }) => await execute(), callId, toolsContext, experimental_sandbox: sandbox, _internal: { generateId: generateId3 = originalGenerateId2, generateCallId = originalGenerateCallId2, now: now2 = now } = {}, onStart, onLanguageModelCallStart, onLanguageModelCallEnd, ...callSettings }) {
	const resolvedModel = resolveLanguageModel(model);
	const effectiveCallId = callId != null ? callId : generateCallId();
	const standardizedPrompt = await standardizePrompt({
		instructions,
		system,
		prompt,
		messages,
		allowSystemInMessages
	});
	const promptMessages = await convertToLanguageModelPrompt({
		prompt: {
			instructions: standardizedPrompt.instructions,
			messages: standardizedPrompt.messages
		},
		supportedUrls: await resolvedModel.supportedUrls,
		download: download2,
		provider: resolvedModel.provider.split(".")[0]
	});
	const stepTools = await prepareTools({
		tools,
		toolOrder,
		toolsContext,
		experimental_sandbox: sandbox
	});
	const stepToolChoice = prepareToolChoice({ toolChoice });
	await notify({
		event: { promptMessages },
		callbacks: onStart
	});
	const languageModelCallStartEvent = {
		callId: effectiveCallId,
		provider: resolvedModel.provider,
		modelId: resolvedModel.modelId,
		instructions: standardizedPrompt.instructions,
		messages: standardizedPrompt.messages,
		tools: stepTools,
		...callSettings
	};
	await notify({
		event: languageModelCallStartEvent,
		callbacks: onLanguageModelCallStart
	});
	const callStartTimestampMs = now2();
	const { stream: languageModelStream, response, request } = await executeLanguageModelCallInTelemetryContext({
		...languageModelCallStartEvent,
		execute: async () => await resolvedModel.doStream({
			...callSettings,
			tools: stepTools,
			toolChoice: stepToolChoice,
			responseFormat: await (output == null ? void 0 : output.responseFormat),
			prompt: promptMessages,
			providerOptions,
			abortSignal,
			headers,
			includeRawChunks
		})
	});
	return {
		stream: createAsyncIterableStream(languageModelStream.pipeThrough(createLanguageModelV4StreamPartToLanguageModelStreamPartTransform({
			tools,
			instructions: standardizedPrompt.instructions,
			messages: standardizedPrompt.messages,
			repairToolCall,
			refineToolInput,
			callId: effectiveCallId,
			provider: resolvedModel.provider,
			modelId: resolvedModel.modelId,
			generateId: generateId3,
			now: now2,
			callStartTimestampMs,
			onLanguageModelCallEnd
		}))),
		response,
		request
	};
}
function createLanguageModelV4StreamPartToLanguageModelStreamPartTransform({ tools, instructions, messages, repairToolCall, refineToolInput, callId, provider, modelId, generateId: generateId3, now: now2, callStartTimestampMs, onLanguageModelCallEnd }) {
	const toolCallsByToolCallId = /* @__PURE__ */ new Map();
	const modelCallContent = [];
	const textPartIndexes = /* @__PURE__ */ new Map();
	const reasoningPartIndexes = /* @__PURE__ */ new Map();
	let responseId = generateId3();
	let responseModelId = modelId;
	let timeToFirstOutputMs;
	let previousOutputChunkTimestampMs;
	const timeBetweenOutputChunksMs = [];
	return new TransformStream({ async transform(chunk, controller) {
		var _a23, _b, _c;
		if (isOutputChunk(chunk)) {
			const outputChunkTimestampMs = now2();
			if (timeToFirstOutputMs == null) timeToFirstOutputMs = outputChunkTimestampMs - callStartTimestampMs;
			else if (previousOutputChunkTimestampMs != null) timeBetweenOutputChunksMs.push(outputChunkTimestampMs - previousOutputChunkTimestampMs);
			previousOutputChunkTimestampMs = outputChunkTimestampMs;
		}
		switch (chunk.type) {
			case "text-start":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: textPartIndexes,
					id: chunk.id,
					type: "text",
					providerMetadata: chunk.providerMetadata
				});
				controller.enqueue(chunk);
				break;
			case "text-delta":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: textPartIndexes,
					id: chunk.id,
					type: "text",
					textDelta: chunk.delta,
					providerMetadata: chunk.providerMetadata
				});
				controller.enqueue({
					type: "text-delta",
					id: chunk.id,
					text: chunk.delta,
					providerMetadata: chunk.providerMetadata
				});
				break;
			case "text-end":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: textPartIndexes,
					id: chunk.id,
					type: "text",
					providerMetadata: chunk.providerMetadata
				});
				textPartIndexes.delete(chunk.id);
				controller.enqueue(chunk);
				break;
			case "reasoning-start":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: reasoningPartIndexes,
					id: chunk.id,
					type: "reasoning",
					providerMetadata: chunk.providerMetadata
				});
				controller.enqueue(chunk);
				break;
			case "reasoning-delta":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: reasoningPartIndexes,
					id: chunk.id,
					type: "reasoning",
					textDelta: chunk.delta,
					providerMetadata: chunk.providerMetadata
				});
				controller.enqueue({
					type: "reasoning-delta",
					id: chunk.id,
					text: chunk.delta,
					providerMetadata: chunk.providerMetadata
				});
				break;
			case "reasoning-end":
				upsertTextContentPart({
					content: modelCallContent,
					partIndexes: reasoningPartIndexes,
					id: chunk.id,
					type: "reasoning",
					providerMetadata: chunk.providerMetadata
				});
				reasoningPartIndexes.delete(chunk.id);
				controller.enqueue(chunk);
				break;
			case "file":
			case "reasoning-file": {
				const file = new DefaultGeneratedFileWithType({
					data: chunk.data.type === "data" ? chunk.data.data : chunk.data.url.toString(),
					mediaType: chunk.mediaType
				});
				modelCallContent.push({
					type: chunk.type,
					file,
					...chunk.providerMetadata != null ? { providerMetadata: chunk.providerMetadata } : {}
				});
				controller.enqueue({
					type: chunk.type,
					file,
					providerMetadata: chunk.providerMetadata
				});
				break;
			}
			case "finish": {
				const usage = asLanguageModelUsage(chunk.usage);
				const responseTimeMs = now2() - callStartTimestampMs;
				const performance = {
					responseTimeMs,
					effectiveOutputTokensPerSecond: calculateTokensPerSecond({
						tokens: usage.outputTokens,
						durationMs: responseTimeMs
					}),
					outputTokensPerSecond: timeToFirstOutputMs == null ? void 0 : calculateTokensPerSecond({
						tokens: usage.outputTokens,
						durationMs: responseTimeMs - timeToFirstOutputMs
					}),
					inputTokensPerSecond: timeToFirstOutputMs == null ? void 0 : calculateTokensPerSecond({
						tokens: usage.inputTokens,
						durationMs: timeToFirstOutputMs
					}),
					effectiveTotalTokensPerSecond: calculateTokensPerSecond({
						tokens: sumTokenCounts(usage.inputTokens, usage.outputTokens),
						durationMs: responseTimeMs
					}),
					timeToFirstOutputMs,
					timeBetweenOutputChunksMs: timeBetweenOutputChunksMs.length > 0 ? calculateOutputChunkTimingStats(timeBetweenOutputChunksMs) : void 0
				};
				await notify({
					event: {
						callId,
						provider,
						modelId: responseModelId,
						finishReason: chunk.finishReason.unified,
						usage,
						content: modelCallContent,
						responseId,
						...chunk.providerMetadata != null ? { providerMetadata: chunk.providerMetadata } : {},
						performance
					},
					callbacks: onLanguageModelCallEnd
				});
				controller.enqueue({
					type: "model-call-end",
					finishReason: chunk.finishReason.unified,
					rawFinishReason: chunk.finishReason.raw,
					usage,
					providerMetadata: chunk.providerMetadata,
					performance
				});
				break;
			}
			case "tool-call":
				try {
					const toolCall = await parseToolCall({
						toolCall: chunk,
						tools,
						repairToolCall,
						refineToolInput,
						instructions,
						messages
					});
					toolCallsByToolCallId.set(toolCall.toolCallId, toolCall);
					controller.enqueue(toolCall);
					modelCallContent.push(toolCall);
					if (toolCall.invalid) {
						if (!toolCall.providerExecuted) controller.enqueue({
							type: "tool-error",
							toolCallId: toolCall.toolCallId,
							toolName: toolCall.toolName,
							input: toolCall.input,
							error: getErrorMessage(toolCall.error),
							dynamic: true,
							title: toolCall.title,
							...toolCall.toolMetadata != null ? { toolMetadata: toolCall.toolMetadata } : {}
						});
						break;
					}
				} catch (error) {
					controller.enqueue({
						type: "error",
						error
					});
				}
				break;
			case "tool-approval-request": {
				const toolCall = toolCallsByToolCallId.get(chunk.toolCallId);
				if (toolCall == null) {
					controller.enqueue({
						type: "error",
						error: new ToolCallNotFoundForApprovalError({
							toolCallId: chunk.toolCallId,
							approvalId: chunk.approvalId
						})
					});
					break;
				}
				const toolApprovalRequest = {
					type: "tool-approval-request",
					approvalId: chunk.approvalId,
					toolCall
				};
				controller.enqueue(toolApprovalRequest);
				modelCallContent.push(toolApprovalRequest);
				break;
			}
			case "tool-result": {
				const toolName = chunk.toolName;
				const toolCall = toolCallsByToolCallId.get(chunk.toolCallId);
				const toolResultPart = chunk.isError ? {
					type: "tool-error",
					toolCallId: chunk.toolCallId,
					toolName,
					input: toolCall == null ? void 0 : toolCall.input,
					providerExecuted: true,
					error: chunk.result,
					dynamic: chunk.dynamic,
					...chunk.providerMetadata != null ? { providerMetadata: chunk.providerMetadata } : {},
					...(toolCall == null ? void 0 : toolCall.toolMetadata) != null ? { toolMetadata: toolCall.toolMetadata } : {}
				} : {
					type: "tool-result",
					toolCallId: chunk.toolCallId,
					toolName,
					input: toolCall == null ? void 0 : toolCall.input,
					output: chunk.result,
					providerExecuted: true,
					dynamic: chunk.dynamic,
					...chunk.providerMetadata != null ? { providerMetadata: chunk.providerMetadata } : {},
					...(toolCall == null ? void 0 : toolCall.toolMetadata) != null ? { toolMetadata: toolCall.toolMetadata } : {}
				};
				controller.enqueue(toolResultPart);
				modelCallContent.push(toolResultPart);
				break;
			}
			case "tool-input-start": {
				const tool2 = getOwn(tools, chunk.toolName);
				controller.enqueue({
					...chunk,
					dynamic: (_a23 = chunk.dynamic) != null ? _a23 : (tool2 == null ? void 0 : tool2.type) === "dynamic",
					title: tool2 == null ? void 0 : tool2.title,
					...(tool2 == null ? void 0 : tool2.metadata) != null ? { toolMetadata: tool2.metadata } : {}
				});
				break;
			}
			case "stream-start":
				controller.enqueue({
					type: "model-call-start",
					warnings: chunk.warnings
				});
				break;
			case "response-metadata":
				responseId = (_b = chunk.id) != null ? _b : responseId;
				responseModelId = (_c = chunk.modelId) != null ? _c : responseModelId;
				controller.enqueue({
					type: "model-call-response-metadata",
					id: chunk.id,
					timestamp: chunk.timestamp,
					modelId: chunk.modelId
				});
				break;
			default:
				if (chunk.type === "custom" || chunk.type === "source") modelCallContent.push(chunk);
				controller.enqueue(chunk);
		}
	} });
}
function isOutputChunk(chunk) {
	return chunk.type === "text-delta" && chunk.delta.length > 0 || chunk.type === "reasoning-delta" && chunk.delta.length > 0 || chunk.type === "tool-input-delta" && chunk.delta.length > 0 || chunk.type === "file" || chunk.type === "reasoning-file" || chunk.type === "tool-call";
}
function calculateOutputChunkTimingStats(timingsMs) {
	const sortedTimingsMs = [...timingsMs].sort((a, b) => a - b);
	const sum = timingsMs.reduce((sum2, timingMs) => sum2 + timingMs, 0);
	return {
		min: sortedTimingsMs[0],
		p10: calculateNearestRankPercentile(sortedTimingsMs, .1),
		median: calculateNearestRankPercentile(sortedTimingsMs, .5),
		avg: sum / timingsMs.length,
		p90: calculateNearestRankPercentile(sortedTimingsMs, .9),
		max: sortedTimingsMs[sortedTimingsMs.length - 1]
	};
}
function calculateNearestRankPercentile(sortedValues, percentile) {
	return sortedValues[Math.ceil(percentile * sortedValues.length) - 1];
}
function upsertTextContentPart({ content, partIndexes, id, type, textDelta, providerMetadata }) {
	let partIndex = partIndexes.get(id);
	if (partIndex == null) {
		partIndex = content.push({
			type,
			text: "",
			...providerMetadata != null ? { providerMetadata } : {}
		}) - 1;
		partIndexes.set(id, partIndex);
	}
	const part = content[partIndex];
	if (textDelta != null) part.text += textDelta;
	if (providerMetadata != null) part.providerMetadata = providerMetadata;
}
createIdGenerator({
	prefix: "aitxt",
	size: 24
});
createIdGenerator({
	prefix: "call",
	size: 24
});
var toolMetadataSchema2 = z.record(z.string(), jsonValueSchema.optional());
var providerReferenceSchema2 = z.record(z.string(), z.string());
lazySchema(() => zodSchema(z.array(z.object({
	id: z.string(),
	role: z.enum([
		"system",
		"user",
		"assistant"
	]),
	metadata: z.unknown().optional(),
	parts: z.array(z.union([
		z.object({
			type: z.literal("text"),
			text: z.string(),
			state: z.enum(["streaming", "done"]).optional(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("reasoning"),
			id: z.string().optional(),
			text: z.string(),
			state: z.enum(["streaming", "done"]).optional(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("custom"),
			kind: z.string(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("source-url"),
			sourceId: z.string(),
			url: z.string(),
			title: z.string().optional(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("source-document"),
			sourceId: z.string(),
			mediaType: z.string(),
			title: z.string(),
			filename: z.string().optional(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("file"),
			mediaType: z.string(),
			filename: z.string().optional(),
			url: z.string(),
			providerReference: providerReferenceSchema2.optional(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({
			type: z.literal("reasoning-file"),
			mediaType: z.string(),
			url: z.string(),
			providerMetadata: providerMetadataSchema.optional()
		}),
		z.object({ type: z.literal("step-start") }),
		z.object({
			type: z.string().startsWith("data-"),
			id: z.string().optional(),
			data: z.unknown()
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("input-streaming"),
			input: z.unknown().optional(),
			providerExecuted: z.boolean().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			approval: z.never().optional()
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("input-available"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.never().optional()
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("approval-requested"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.never().optional(),
				reason: z.never().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("approval-responded"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.boolean(),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-available"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.unknown(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			resultProviderMetadata: providerMetadataSchema.optional(),
			preliminary: z.boolean().optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(true),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			}).optional()
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-error"),
			input: z.unknown().optional(),
			rawInput: z.unknown().optional(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.string(),
			callProviderMetadata: providerMetadataSchema.optional(),
			resultProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(true),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			}).optional()
		}),
		z.object({
			type: z.literal("dynamic-tool"),
			toolName: z.string(),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-denied"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(false),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("input-streaming"),
			providerExecuted: z.boolean().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			input: z.unknown().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			approval: z.never().optional()
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("input-available"),
			providerExecuted: z.boolean().optional(),
			input: z.unknown(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.never().optional()
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("approval-requested"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.never().optional(),
				reason: z.never().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("approval-responded"),
			input: z.unknown(),
			providerExecuted: z.boolean().optional(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.boolean(),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-available"),
			providerExecuted: z.boolean().optional(),
			input: z.unknown(),
			output: z.unknown(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			resultProviderMetadata: providerMetadataSchema.optional(),
			preliminary: z.boolean().optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(true),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			}).optional()
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-error"),
			providerExecuted: z.boolean().optional(),
			input: z.unknown().optional(),
			rawInput: z.unknown().optional(),
			output: z.never().optional(),
			errorText: z.string(),
			callProviderMetadata: providerMetadataSchema.optional(),
			resultProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(true),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			}).optional()
		}),
		z.object({
			type: z.string().startsWith("tool-"),
			toolCallId: z.string(),
			toolMetadata: toolMetadataSchema2.optional(),
			state: z.literal("output-denied"),
			providerExecuted: z.boolean().optional(),
			input: z.unknown(),
			output: z.never().optional(),
			errorText: z.never().optional(),
			callProviderMetadata: providerMetadataSchema.optional(),
			approval: z.object({
				id: z.string(),
				approved: z.literal(false),
				reason: z.string().optional(),
				isAutomatic: z.boolean().optional(),
				signature: z.string().optional()
			})
		})
	]))
}).superRefine((message, context) => {
	if (message.role !== "assistant" && message.parts.length === 0) context.addIssue({
		origin: "array",
		code: "too_small",
		minimum: 1,
		inclusive: true,
		input: message.parts,
		path: ["parts"],
		message: "Message must contain at least one part"
	});
})).nonempty("Messages array must not be empty")));
createIdGenerator({
	prefix: "call",
	size: 24
});
createIdGenerator({
	prefix: "call",
	size: 24
});
createIdGenerator({
	prefix: "aiobj",
	size: 24
});
createIdGenerator({
	prefix: "aiobj",
	size: 24
});
createIdGenerator({
	prefix: "call",
	size: 24
});
//#endregion
export { streamLanguageModelCall as t };
