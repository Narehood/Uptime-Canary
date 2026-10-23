/**
 * Minimal QR Code encoder (ISO/IEC 18004): byte mode, error correction level M, versions 1–40, automatic mask.
 * Follows the structure of Project Nayuki's reference generator (MIT). It runs entirely in the browser so an
 * authenticator seed never reaches a third-party QR service.
 */

// Index = version. Level M only.
const ECC_PER_BLOCK = [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28];
const BLOCKS = [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49];
/** Format bits for level M. */
const ECL_BITS = 0;

export function rawDataModules(version: number): number {
  let result = (16 * version + 128) * version + 64;
  if (version >= 2) {
    const align = Math.floor(version / 7) + 2;
    result -= (25 * align - 10) * align - 55;
    if (version >= 7) result -= 36;
  }
  return result;
}

export const dataCodewords = (version: number): number => Math.floor(rawDataModules(version) / 8) - ECC_PER_BLOCK[version]! * BLOCKS[version]!;

export function gfMultiply(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z & 0xff;
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMultiply(result[j]!, root);
      if (j + 1 < result.length) result[j]! ^= result[j + 1]!;
    }
    root = gfMultiply(root, 0x02);
  }
  return result;
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result = new Array<number>(divisor.length).fill(0);
  for (const byte of data) {
    const factor = byte ^ result.shift()!;
    result.push(0);
    divisor.forEach((coefficient, index) => { result[index]! ^= gfMultiply(coefficient, factor); });
  }
  return result;
}

export function alignmentPositions(version: number): number[] {
  if (version === 1) return [];
  const count = Math.floor(version / 7) + 2;
  const step = Math.floor((version * 8 + count * 3 + 5) / (count * 4 - 4)) * 2;
  const result = [6];
  for (let pos = version * 4 + 17 - 7; result.length < count; pos -= step) result.splice(1, 0, pos);
  return result;
}

export function formatBits(mask: number): number {
  const data = (ECL_BITS << 3) | mask;
  let rem = data;
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
  return ((data << 10) | rem) ^ 0x5412;
}

export function versionBits(version: number): number {
  let rem = version;
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
  return (version << 12) | rem;
}

const bit = (value: number, index: number): boolean => ((value >>> index) & 1) !== 0;

function maskApplies(mask: number, x: number, y: number): boolean {
  switch (mask) {
    case 0: return (x + y) % 2 === 0;
    case 1: return y % 2 === 0;
    case 2: return x % 3 === 0;
    case 3: return (x + y) % 3 === 0;
    case 4: return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
    case 5: return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
}

/** Data codewords followed by interleaved Reed–Solomon codewords, ready for placement. */
export function codewords(bytes: Uint8Array, version: number): number[] {
  const capacity = dataCodewords(version);
  const bits: number[] = [];
  const push = (value: number, length: number): void => { for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1); };
  push(0b0100, 4);
  push(bytes.length, version <= 9 ? 8 : 16);
  for (const byte of bytes) push(byte, 8);
  push(0, Math.min(4, capacity * 8 - bits.length));
  push(0, (8 - (bits.length % 8)) % 8);
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) data.push(bits.slice(i, i + 8).reduce((acc, b) => (acc << 1) | b, 0));
  for (let pad = 0xec; data.length < capacity; pad ^= 0xec ^ 0x11) data.push(pad);

  const blocks = BLOCKS[version]!;
  const eccLength = ECC_PER_BLOCK[version]!;
  const raw = Math.floor(rawDataModules(version) / 8);
  const shortBlocks = blocks - (raw % blocks);
  const shortLength = Math.floor(raw / blocks);
  const divisor = rsDivisor(eccLength);
  const parts: number[][] = [];
  for (let i = 0, k = 0; i < blocks; i++) {
    const block = data.slice(k, k + shortLength - eccLength + (i < shortBlocks ? 0 : 1));
    k += block.length;
    const ecc = rsRemainder(block, divisor);
    if (i < shortBlocks) block.push(0);
    parts.push(block.concat(ecc));
  }
  const result: number[] = [];
  for (let i = 0; i < parts[0]!.length; i++) {
    parts.forEach((part, j) => { if (i !== shortLength - eccLength || j >= shortBlocks) result.push(part[i]!); });
  }
  return result;
}

export interface QrCode { size: number; version: number; mask: number; dark: (x: number, y: number) => boolean }

export function encodeQr(text: string): QrCode {
  const bytes = new TextEncoder().encode(text);
  let version = 1;
  while (version <= 40 && 4 + (version <= 9 ? 8 : 16) + bytes.length * 8 > dataCodewords(version) * 8) version++;
  if (version > 40) throw new Error('Text too long for a QR code');
  const size = version * 4 + 17;
  const modules = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const fixed = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const set = (x: number, y: number, dark: boolean): void => { modules[y]![x] = dark; fixed[y]![x] = true; };

  for (let i = 0; i < size; i++) { set(6, i, i % 2 === 0); set(i, 6, i % 2 === 0); }
  for (const [cx, cy] of [[3, 3], [size - 4, 3], [3, size - 4]] as const) {
    for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) {
      const x = cx + dx, y = cy + dy;
      if (x < 0 || x >= size || y < 0 || y >= size) continue;
      const distance = Math.max(Math.abs(dx), Math.abs(dy));
      set(x, y, distance !== 2 && distance !== 4);
    }
  }
  const align = alignmentPositions(version);
  align.forEach((ax, i) => align.forEach((ay, j) => {
    if ((i === 0 && j === 0) || (i === 0 && j === align.length - 1) || (i === align.length - 1 && j === 0)) return;
    for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(ax + dx, ay + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  }));
  const drawFormat = (mask: number): void => {
    const bits = formatBits(mask);
    for (let i = 0; i <= 5; i++) set(8, i, bit(bits, i));
    set(8, 7, bit(bits, 6));
    set(8, 8, bit(bits, 7));
    set(7, 8, bit(bits, 8));
    for (let i = 9; i < 15; i++) set(14 - i, 8, bit(bits, i));
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(bits, i));
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(bits, i));
    set(8, size - 8, true);
  };
  drawFormat(0);
  if (version >= 7) {
    const bits = versionBits(version);
    for (let i = 0; i < 18; i++) {
      const a = size - 11 + (i % 3), b = Math.floor(i / 3);
      set(a, b, bit(bits, i));
      set(b, a, bit(bits, i));
    }
  }

  const data = codewords(bytes, version);
  let index = 0;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vertical = 0; vertical < size; vertical++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const y = ((right + 1) & 2) === 0 ? size - 1 - vertical : vertical;
        if (!fixed[y]![x] && index < data.length * 8) {
          modules[y]![x] = bit(data[index >>> 3]!, 7 - (index & 7));
          index++;
        }
      }
    }
  }

  const toggle = (mask: number): void => {
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!fixed[y]![x] && maskApplies(mask, x, y)) modules[y]![x] = !modules[y]![x];
  };
  let best = 0, bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    toggle(mask);
    drawFormat(mask);
    const score = penalty(modules);
    if (score < bestScore) { best = mask; bestScore = score; }
    toggle(mask);
  }
  toggle(best);
  drawFormat(best);
  return { size, version, mask: best, dark: (x, y) => modules[y]![x]! };
}

/** ISO penalty rules: runs, 2×2 blocks, finder-like patterns, and dark/light balance. */
function penalty(modules: boolean[][]): number {
  const size = modules.length;
  let score = 0;
  const line = (get: (i: number) => boolean): void => {
    let run = 1;
    for (let i = 1; i <= size; i++) {
      if (i < size && get(i) === get(i - 1)) { run++; continue; }
      if (run >= 5) score += run - 2;
      run = 1;
    }
    for (let i = 0; i + 11 <= size; i++) {
      const window = Array.from({ length: 11 }, (_, k) => get(i + k));
      const core = [true, false, true, true, true, false, true];
      const matches = (offset: number): boolean => core.every((value, k) => window[offset + k] === value);
      if ((matches(0) && window.slice(7).every(v => !v)) || (matches(4) && window.slice(0, 4).every(v => !v))) score += 40;
    }
  };
  for (let y = 0; y < size; y++) line(x => modules[y]![x]!);
  for (let x = 0; x < size; x++) line(y => modules[y]![x]!);
  for (let y = 0; y + 1 < size; y++) for (let x = 0; x + 1 < size; x++) {
    const c = modules[y]![x];
    if (c === modules[y]![x + 1] && c === modules[y + 1]![x] && c === modules[y + 1]![x + 1]) score += 3;
  }
  const dark = modules.reduce((sum, row) => sum + row.filter(Boolean).length, 0);
  const total = size * size;
  score += Math.floor(Math.abs(dark * 20 - total * 10) / total) * 10;
  return score;
}
