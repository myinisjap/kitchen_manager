// Kitchen-style quantity display and input: 1.333 cup shows as "1⅓ cup" and
// "1 1/3" (or "1⅓") can be typed into quantity fields. Stored values stay
// plain numbers; this only changes how they are shown and read.

// Metric units read naturally as decimals (250 g, 1.5 L), so they never get
// fractions. Everything else (cups, spoons, oz, lb, counts, no unit) does.
const DECIMAL_UNITS = new Set(['g', 'kg', 'ml', 'L']);

// Fractions a cook measures with: halves, thirds, quarters, eighths.
const FRACTIONS = [
  [1, 8, '⅛'], [1, 4, '¼'], [1, 3, '⅓'], [3, 8, '⅜'], [1, 2, '½'],
  [5, 8, '⅝'], [2, 3, '⅔'], [3, 4, '¾'], [7, 8, '⅞'],
];

const GLYPHS = Object.fromEntries(FRACTIONS.map(([n, d, g]) => [g, n / d]));

// How far a value may be from a fraction and still be shown as it. Wide
// enough to catch values already rounded to 2 places (1.33, 0.67), narrow
// enough that 0.1 is not passed off as ⅛.
const TOLERANCE = 0.02;

function decimal(n) {
  return parseFloat(n.toFixed(2)).toString();
}

// fractionParts returns [whole, fraction] for n when it is close to a whole
// number or a kitchen fraction, else null. fraction is null for whole numbers.
function fractionParts(n) {
  let whole = Math.floor(n);
  const rest = n - whole;
  if (rest < TOLERANCE) return [whole, null];
  if (1 - rest < TOLERANCE) return [whole + 1, null];
  for (const f of FRACTIONS) {
    if (Math.abs(rest - f[0] / f[1]) < TOLERANCE) return [whole, f];
  }
  return null;
}

function format(n, unit, ascii) {
  if (n === null || n === undefined || n === '') return '';
  n = Number(n);
  if (!Number.isFinite(n)) return '';
  if (DECIMAL_UNITS.has(unit) || n < 0) return decimal(n);
  const parts = fractionParts(n);
  if (!parts) return decimal(n);
  const [whole, f] = parts;
  if (!f) return String(whole);
  const frac = ascii ? f[0] + '/' + f[1] : f[2];
  if (whole === 0) return frac;
  return whole + (ascii ? ' ' : '') + frac;
}

// parseQty reads "1.5", "1/2", "1 1/3", "1-1/3", "1⅓" or "⅓" and returns the
// number, or null when the text is not a quantity. Empty text is null too.
export function parseQty(text) {
  if (typeof text === 'number') return Number.isFinite(text) ? text : null;
  let s = String(text ?? '').trim();
  if (!s) return null;
  let glyph = 0;
  const last = s.slice(-1);
  if (Object.hasOwn(GLYPHS, last)) {
    glyph = GLYPHS[last];
    s = s.slice(0, -1).trim();
    if (!s) return glyph;
  }
  let m = s.match(/^(\d+)(?:\s+|\s*-\s*)(\d+)\s*\/\s*(\d+)$/);
  if (m && !glyph) {
    const d = Number(m[3]);
    return d ? Number(m[1]) + Number(m[2]) / d : null;
  }
  m = s.match(/^(\d+)\s*\/\s*(\d+)$/);
  if (m && !glyph) {
    const d = Number(m[2]);
    return d ? Number(m[1]) / d : null;
  }
  if (!/^(\d+\.?\d*|\.\d+)$/.test(s)) return null;
  return Number(s) + glyph;
}

export function qty() {
  return {
    // formatQty shows a quantity for reading: "1⅓" for cups, "250" for grams.
    formatQty(n, unit) {
      return format(n, unit, false);
    },

    // qtyInput shows a quantity in an editable field, with typeable
    // fractions ("1 1/3").
    qtyInput(n, unit) {
      return format(n, unit, true);
    },

    // setQty is the @change handler for a quantity field: it stores the parsed
    // number in obj[key] and rewrites the field. Empty text stores '' (as
    // x-model.number did); text that is not a quantity keeps the old value.
    setQty(obj, key, el, unit) {
      const text = el.value.trim();
      if (!text) {
        obj[key] = '';
      } else {
        const v = parseQty(text);
        if (v === null) this.showToast('Not a quantity: ' + text, 'error');
        else obj[key] = v;
      }
      el.value = format(obj[key], unit, true);
    },
  };
}
