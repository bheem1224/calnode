import fallback from './timezone-fallback.json';

const aliases: Record<string, string[]> = {
  'Asia/Kolkata': ['IST', 'India', 'Calcutta'],
  'America/New_York': ['EST', 'EDT', 'Eastern'],
  'America/Chicago': ['CST', 'CDT', 'Central'],
  'America/Denver': ['MST', 'MDT', 'Mountain'],
  'America/Los_Angeles': ['PST', 'PDT', 'Pacific'],
  'Europe/Paris': ['CET', 'CEST'],
  'Europe/Berlin': ['CET', 'CEST'],
  'Europe/London': ['GMT', 'BST'],
  'Asia/Tokyo': ['JST'], 'Asia/Singapore': ['SGT'], UTC: ['GMT']
};

// Include canonical names and UTC even where ICU reports a legacy alias.
export function timezoneOptions(current = '') {
  let zones: string[];
  try { zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : fallback; }
  catch { zones = fallback; }
  const names = [...new Set([...zones, ...fallback.filter(z => ['Asia/Kolkata', 'Asia/Tokyo', 'Asia/Singapore', 'Europe/London', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles'].includes(z)), 'Asia/Kolkata', 'UTC', current].filter(Boolean))].sort();
  const now = new Date();
  return names.map(value => {
    let offset = 'UTC';
    try {
      const part = new Intl.DateTimeFormat('en-US', { timeZone: value, timeZoneName: 'shortOffset' }).formatToParts(now).find(p => p.type === 'timeZoneName')?.value || 'GMT';
      const match = part.match(/GMT([+-])(\d{1,2})(?::(\d{2}))?/);
      if (match) offset = `UTC${match[1]}${match[2].padStart(2, '0')}:${match[3] || '00'}`;
    } catch { /* Preserve a previously saved timezone even if this ICU does not know it. */ }
    const abbreviation = aliases[value]?.[0];
    const compactOffset = offset.replace(/^UTC/, 'GMT').replace(/([+-])0(\d)/, '$1$2');
    return { value, label: `${value} (${abbreviation ? abbreviation + ', ' : ''}${offset})`, keywords: [...(aliases[value] || []), value.replaceAll('_', ' '), compactOffset, offset.replace(/^UTC/, 'GMT')].join(' ') };
  });
}
