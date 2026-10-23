// System service checks (protocol 16): each assigned node reads one named service from its own operating system's
// service manager, systemd on Linux and the Service Control Manager on Windows. The monitor's target is the whole
// configuration: no options object and no credentials. Pure: no JSX, no network.

/** The node's limit. Every allowed character is ASCII, so characters and bytes agree. */
export const SERVICE_NAME_MAX = 256;

/**
 * Mirrors the node's rule without being stricter: 1–256 ASCII bytes, the first a letter, digit or underscore, the rest
 * also dots, hyphens and @. Each message names the first problem found, most specific first.
 */
export function serviceNameProblem(name: string): string | undefined {
  if (!name) return 'Enter the internal service name, like sshd.service or Spooler.';
  if (/\s/u.test(name)) return 'Service names have no spaces. Use the internal name (Spooler), not the display name (Print Spooler).';
  if (/[^\x21-\x7e]/u.test(name)) return 'Use only ASCII letters, digits, dots, underscores, hyphens and @.';
  if (/[/\\]/u.test(name)) return 'Enter a service name, not a path.';
  if (/[*?\[\]]/u.test(name)) return 'Patterns aren’t supported: name exactly one service.';
  if (/["'`]/u.test(name)) return 'Remove the quotes and enter the name alone, like sshd.service.';
  if (!/^[A-Za-z0-9_]/u.test(name)) return 'Start with a letter, digit or underscore, not a dash, dot or @.';
  const bad = /[^A-Za-z0-9_.@-]/u.exec(name);
  if (bad) return `“${bad[0]}” isn’t allowed in a service name. Use letters, digits, dots, underscores, hyphens and @.`;
  if (name.length > SERVICE_NAME_MAX) return `Use at most ${SERVICE_NAME_MAX} characters.`;
  return undefined;
}

/** The unit a Linux node asks systemd about: `.service` is appended unless the name already ends with it. */
export const systemdUnit = (name: string): string => name.endsWith('.service') ? name : `${name}.service`;
