/**
 * How big a model request is, for billing's admission hold: the text bytes, and the images
 * counted apart. A screenshot rides in the messages as megabytes of base64 but costs a couple
 * of thousand tokens, so sizing it by bytes held more than a whole 5-hour window for one turn
 * and refused the run's next step right after it looked at the screen.
 */
const IMAGE_PAYLOAD_CHARS = 20_000;

export function admissionInput(value: unknown): { input_bytes: number; input_images?: number } {
  let images = 0;
  const text = JSON.stringify(value, (_key, item) => {
    // Raw bytes (a Buffer or typed array) are image or file data; JSON would spell out every byte.
    if (item instanceof Uint8Array || item instanceof ArrayBuffer) {
      images++;
      return "";
    }
    if (typeof item === "string" && item.length >= IMAGE_PAYLOAD_CHARS && (item.startsWith("data:") || /^[A-Za-z0-9+/_=-]{256}/.test(item))) {
      images++;
      return "";
    }
    return item;
  });
  const bytes = new TextEncoder().encode(text ?? "").byteLength;
  return images > 0 ? { input_bytes: bytes, input_images: images } : { input_bytes: bytes };
}
