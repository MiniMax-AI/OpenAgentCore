import { createMDX } from "fumadocs-mdx/next"
import { fileURLToPath } from "node:url"

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  // Keep the listener's origin for local rewrites. Normalizing 127.0.0.1 to
  // localhost makes Next proxy the rewrite and re-enter the locale redirect.
  skipMiddlewareUrlNormalize: true,
  outputFileTracingRoot: fileURLToPath(new URL("../../", import.meta.url)),
  // Keep the Next.js development overlay indicator out of the product UI.
  devIndicators: false,
}

export default withMDX(config)
