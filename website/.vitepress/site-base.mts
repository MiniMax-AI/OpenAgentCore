/** Local builds use the root; published builds derive their path from the Pages URL. */
export function siteBase(value: string | undefined): string {
  if (value === undefined) return '/'

  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new Error('WEBSITE_URL must be an absolute HTTP(S) URL')
  }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('WEBSITE_URL must be an absolute HTTP(S) URL without credentials, query or fragment')
  }
  return url.pathname.endsWith('/') ? url.pathname : `${url.pathname}/`
}
