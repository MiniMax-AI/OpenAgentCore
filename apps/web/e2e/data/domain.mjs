/** Same-origin installation manager fixture; readiness changes only through test controls. */
export function domainState() {
  return { supported: true, state: "unconfigured", public_url: null, target_url: null, message: null };
}

export async function domainRoute(request, response, state, { send, error, body }) {
  if (request.method === "GET") return send(response, 200, state.domain);
  if (request.method !== "POST") return error(response, 405, "Method not allowed.");
  const input = await body(request);
  if (!state.domain.supported) return error(response, 409, "Domain setup is unavailable.", "unsupported");
  const target = `https://${input.hostname}`;
  state.writes.push("POST /console/installation/domain");
  if (state.domain.public_url && state.domain.public_url !== target && input.confirm_public_url_change !== target) {
    return error(response, 409, "Confirm the new public address.", "public_url_confirmation_required");
  }
  Object.assign(state.domain, { state: "checking", target_url: target, message: null });
  return send(response, 202, state.domain);
}
