/**
 * Shared client-side navigation primitive for the URL-is-SSOT mini-router.
 *
 * `window.location.pathname` is the single source of truth for the active view;
 * `navigate()` mutates history (push or replace) and synchronously dispatches a
 * `popstate` event so the App re-renders against the new path. It is defined
 * here (rather than inside App) so the admin hub and subpages — which are
 * props-less default exports per the routing contract — can navigate without
 * receiving a callback prop.
 */
export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (opts.replace) window.history.replaceState({ __appNav: true }, "", to);
  else window.history.pushState({ __appNav: true }, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
