import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// The Pi logo is the unmodified official asset, not a generic Greek π glyph.
// See landing/public/UPSTREAM-NOTICE for upstream source and attribution.
test("Pi logo matches the verified official brand SVG", () => {
  const svg = readFileSync(new URL("../public/agents/pi.svg", import.meta.url));
  assert.equal(
    createHash("sha256").update(svg).digest("hex"),
    "abd66e7868b2d24f0f0895f9237ee8a6dcb22337583b0dc54aeb595acecb4d6b",
  );
});
