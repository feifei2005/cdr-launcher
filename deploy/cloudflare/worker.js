// Cloudflare Worker：只允许 ESA 回源取 /pack/*，并直读私有 R2 桶 cdr-pack。
// 鉴权：请求头 x-cdr-origin-key 必须与 secret ORIGIN_KEY 一致（常量时间比较）。
// R2 对象键与 URL 路径一致（例如 /pack/blobs/<sha> -> 键 pack/blobs/<sha>）。

const AUTH_HEADER = "x-cdr-origin-key";
const PREFIX = "/pack/";
const MANIFEST_KEY = "pack/manifest.json";
const LAUNCHER_MANIFEST_KEY = "pack/launcher/latest.json";

function constantTimeEqual(a, b) {
  const encoder = new TextEncoder();
  const x = encoder.encode(a);
  const y = encoder.encode(b);
  if (x.length !== y.length) return false;
  let diff = 0;
  for (let i = 0; i < x.length; i++) diff |= x[i] ^ y[i];
  return diff === 0;
}

// 解析单区间 Range；返回 null 表示忽略，返回 "unsatisfiable" 表示 416。
function parseRange(header, size) {
  const match = /^bytes=(\d*)-(\d*)$/.exec(header.trim());
  if (!match) return null;
  const startRaw = match[1];
  const endRaw = match[2];
  if (startRaw === "" && endRaw === "") return null;
  let start;
  let end;
  if (startRaw === "") {
    const suffix = parseInt(endRaw, 10);
    if (!suffix) return null;
    start = Math.max(0, size - suffix);
    end = size - 1;
  } else {
    start = parseInt(startRaw, 10);
    end = endRaw === "" ? size - 1 : parseInt(endRaw, 10);
  }
  if (start > end || start >= size) return "unsatisfiable";
  end = Math.min(end, size - 1);
  return { offset: start, end, length: end - start + 1 };
}

export default {
  async fetch(request, env) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("Method Not Allowed", { status: 405 });
    }
    const url = new URL(request.url);
    if (!url.pathname.startsWith(PREFIX)) {
      return new Response("Not Found", { status: 404 });
    }
    const provided = request.headers.get(AUTH_HEADER) || "";
    if (!env.ORIGIN_KEY || !constantTimeEqual(provided, env.ORIGIN_KEY)) {
      return new Response("Not Found", { status: 404 });
    }
    const key = decodeURIComponent(url.pathname.slice(1));
    if (!key || key.includes("..")) {
      return new Response("Not Found", { status: 404 });
    }

    const head = await env.BUCKET.head(key);
    if (!head) return new Response("Not Found", { status: 404 });

    const headers = new Headers();
    head.writeHttpMetadata(headers);
    headers.set("etag", head.httpEtag);
    headers.set("accept-ranges", "bytes");
    headers.set(
      "cache-control",
      key === MANIFEST_KEY || key === LAUNCHER_MANIFEST_KEY
        ? "public, max-age=30"
        : "public, max-age=31536000, immutable"
    );

    const rangeHeader = request.headers.get("range");
    let range = null;
    if (rangeHeader) {
      const parsed = parseRange(rangeHeader, head.size);
      if (parsed === "unsatisfiable") {
        headers.set("content-range", `bytes */${head.size}`);
        return new Response(null, { status: 416, headers });
      }
      range = parsed;
    }

    if (request.method === "HEAD") {
      headers.set("content-length", String(range ? range.length : head.size));
      if (range) headers.set("content-range", `bytes ${range.offset}-${range.end}/${head.size}`);
      return new Response(null, { status: range ? 206 : 200, headers });
    }

    const object = await env.BUCKET.get(
      key,
      range ? { range: { offset: range.offset, length: range.length } } : undefined
    );
    if (!object) return new Response("Not Found", { status: 404 });

    headers.set("content-length", String(range ? range.length : head.size));
    if (range) headers.set("content-range", `bytes ${range.offset}-${range.end}/${head.size}`);
    return new Response(object.body, { status: range ? 206 : 200, headers });
  },
};
