import type { BrowserScreenshot } from '@uptime-canary/shared';
import { validProjectionTime } from './publicAvailability';

/**
 * Private real-browser screenshots (peer protocol 19). A node keeps at most one per monitor: the newest PNG it captured
 * itself, only for the monitor's current check generation, never replicated, published or given to viewers. Editors and
 * admins read it from that node with GET /api/monitors/<id>/screenshot. The node checks every image before storing it;
 * the dashboard checks the answer again before it becomes a data: URL, so nothing but a PNG of the node's fixed size
 * can reach an <img>. Pure: no React, no network.
 */

export const SCREENSHOT_WIDTH = 1024;
export const SCREENSHOT_HEIGHT = 576;
/** The node's limit for one stored image, in bytes. */
export const SCREENSHOT_MAX_BYTES = 192 * 1024;
/** The standard base64 length of the largest image, as Go's encoding/json writes a []byte. */
export const SCREENSHOT_MAX_BASE64 = Math.ceil(SCREENSHOT_MAX_BYTES / 3) * 4;

const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/u;
/** The PNG signature, then the IHDR chunk's length (13) and type: the first 16 bytes of every PNG the node accepts. */
const PNG_START = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52];
const uint32 = (bytes: number[], at: number): number => ((bytes[at]! << 24) >>> 0) + (bytes[at + 1]! << 16) + (bytes[at + 2]! << 8) + bytes[at + 3]!;

/**
 * Whether `data` is standard padded base64 of at most 192 KiB that starts like a 1024×576 PNG: the signature, then an
 * IHDR chunk with that width and height. The node has already decoded the whole image; this is a second, cheap guard.
 */
export function pngData(data: string): boolean {
  if (data.length < 32 || data.length % 4 !== 0 || data.length > SCREENSHOT_MAX_BASE64 || !BASE64.test(data)) return false;
  const padding = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0;
  if ((data.length / 4) * 3 - padding > SCREENSHOT_MAX_BYTES) return false;
  let head: number[];
  try { head = Array.from(atob(data.slice(0, 32)), char => char.charCodeAt(0)); } catch { return false; }
  return PNG_START.every((byte, index) => head[index] === byte) && uint32(head, 16) === SCREENSHOT_WIDTH && uint32(head, 20) === SCREENSHOT_HEIGHT;
}

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const plainText = (value: unknown): value is string => typeof value === 'string' && value !== '' && !/[\x00-\x1f\x7f]/u.test(value);

/**
 * The node's answer for `monitorId`, or undefined when it isn't one: another monitor's image, a missing or wrong field,
 * a time a Date can't hold, or data that isn't the PNG described above. Fields a newer node may add are left out.
 */
export function readScreenshot(value: unknown, monitorId: string): BrowserScreenshot | undefined {
  if (!isRecord(value) || value.monitorId !== monitorId || value.mimeType !== 'image/png') return undefined;
  const { nodeId, checkGeneration, checkedAt, current, data } = value;
  if (!plainText(nodeId) || !plainText(checkGeneration) || !validProjectionTime(checkedAt) || typeof current !== 'boolean') return undefined;
  if (typeof data !== 'string' || !pngData(data)) return undefined;
  return { monitorId, nodeId, checkGeneration, checkedAt, current, mimeType: 'image/png', data };
}

/** The image as a data: URL. Only a checked screenshot gets one, and only inside the signed-in monitor page. */
export const screenshotSrc = (shot: BrowserScreenshot): string => `data:image/png;base64,${shot.data}`;
