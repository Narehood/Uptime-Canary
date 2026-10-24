import { useEffect, useState } from 'react';

/** Hash routes keep the static web container simple and never put API data or credentials in the URL. */
export type Route =
  | { name: 'overview' }
  | { name: 'monitors' }
  | { name: 'monitor-new' }
  | { name: 'monitor'; id: string }
  | { name: 'monitor-edit'; id: string }
  | { name: 'tags' }
  | { name: 'groups' }
  | { name: 'group-new' }
  | { name: 'group-edit'; id: string }
  | { name: 'incidents' }
  | { name: 'notifications' }
  | { name: 'deliveries' }
  | { name: 'receipts' }
  | { name: 'channel-new' }
  | { name: 'channel-edit'; id: string }
  | { name: 'maintenance' }
  | { name: 'maintenance-new' }
  | { name: 'maintenance-edit'; id: string }
  | { name: 'status-pages' }
  | { name: 'status-page-new' }
  | { name: 'status-page-edit'; id: string }
  /**
   * Public, credential-free status page. `nodes` are node base addresses chosen by whoever shared the link. On the
   * control plane `registration` instead names the saved node the dashboard's server reads it from.
   */
  | { name: 'public-status'; slug: string; nodes: string[]; registration?: string }
  | { name: 'nodes' }
  | { name: 'config' }
  | { name: 'accounts' }
  | { name: 'account' }
  | { name: 'not-found'; path: string };

export function parseRoute(hash: string): Route {
  const raw = hash.replace(/^#/u, '') || '/overview';
  const [path = '', query = ''] = raw.split('?', 2) as [string, string?];
  const parts = path.split('/').filter(Boolean).map(part => {
    try { return decodeURIComponent(part); } catch { return part; }
  });
  const [section, id, action] = parts;
  if (section === 'status' && id && parts.length === 2) {
    const params = new URLSearchParams(query);
    const nodes = params.getAll('node').filter(Boolean).slice(0, 8);
    const registration = params.get('registration');
    return registration ? { name: 'public-status', slug: id, nodes, registration } : { name: 'public-status', slug: id, nodes };
  }
  if (query) return { name: 'not-found', path: raw };
  switch (section) {
    case undefined:
    case 'overview': return parts.length <= 1 ? { name: 'overview' } : { name: 'not-found', path: raw };
    case 'monitors':
      if (!id) return { name: 'monitors' };
      if (id === 'new' && !action) return { name: 'monitor-new' };
      if (!action) return { name: 'monitor', id };
      if (action === 'edit' && parts.length === 3) return { name: 'monitor-edit', id };
      break;
    case 'groups':
      if (!id) return { name: 'groups' };
      if (id === 'new' && !action) return { name: 'group-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'group-edit', id };
      break;
    case 'notifications':
      if (!id) return { name: 'notifications' };
      if (id === 'deliveries' && !action) return { name: 'deliveries' };
      if (id === 'receipts' && !action) return { name: 'receipts' };
      break;
    case 'channels':
      if (id === 'new' && !action) return { name: 'channel-new' };
      if (id && action === 'edit' && parts.length === 3) return { name: 'channel-edit', id };
      break;
    case 'maintenance':
      if (!id) return { name: 'maintenance' };
      if (id === 'new' && !action) return { name: 'maintenance-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'maintenance-edit', id };
      break;
    case 'status-pages':
      if (!id) return { name: 'status-pages' };
      if (id === 'new' && !action) return { name: 'status-page-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'status-page-edit', id };
      break;
    case 'incidents': case 'nodes': case 'config': case 'accounts': case 'account': case 'tags':
      if (parts.length === 1) return { name: section };
      break;
  }
  return { name: 'not-found', path: raw };
}

export const href = (...parts: string[]): string => `#/${parts.map(encodeURIComponent).join('/')}`;

/**
 * Shareable public status link. The node address is public information; no credential ever enters it. A control-plane
 * link names a saved registration instead, which is a public selector, not a credential.
 */
export function publicHref(slug: string, nodes: string[], registration?: string): string {
  const query = new URLSearchParams();
  for (const node of nodes) query.append('node', node);
  if (registration) query.set('registration', registration);
  const text = query.toString();
  return `#/status/${encodeURIComponent(slug)}${text ? `?${text}` : ''}`;
}

export function navigate(target: string): void {
  if (location.hash !== target) location.hash = target;
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  useEffect(() => {
    const update = (): void => setRoute(parseRoute(location.hash));
    window.addEventListener('hashchange', update);
    return () => window.removeEventListener('hashchange', update);
  }, []);
  return route;
}

/** Top-level section for rail highlighting. */
export function sectionOf(route: Route): string {
  switch (route.name) {
    case 'monitor': case 'monitor-new': case 'monitor-edit': case 'tags': return 'monitors';
    case 'group-new': case 'group-edit': return 'groups';
    case 'deliveries': case 'receipts': case 'channel-new': case 'channel-edit': return 'notifications';
    case 'maintenance-new': case 'maintenance-edit': return 'maintenance';
    case 'status-page-new': case 'status-page-edit': return 'status-pages';
    case 'not-found': case 'public-status': return '';
    default: return route.name;
  }
}
