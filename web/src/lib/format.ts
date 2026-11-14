import type { Kind, Quantiles } from './api';

export type System = 'metric' | 'imperial';

/** Forecast times are wall-clock strings in the place's own timezone
 *  ("2026-11-13T09:00"). Parse them as UTC and always format as UTC so the
 *  viewer's timezone never shifts them. */
export function parseLocal(s: string): Date {
	return new Date(s.length === 10 ? `${s}T00:00Z` : `${s}Z`);
}

const fmt = (opts: Intl.DateTimeFormatOptions) =>
	new Intl.DateTimeFormat(undefined, { timeZone: 'UTC', ...opts });

const weekdayF = fmt({ weekday: 'short' });
const dayNumF = fmt({ day: 'numeric' });
const monthDayF = fmt({ month: 'short', day: 'numeric' });
const hourF = fmt({ hour: 'numeric' });

export const weekday = (s: string) => weekdayF.format(parseLocal(s));
export const dayNum = (s: string) => dayNumF.format(parseLocal(s));
export const monthDay = (s: string) => monthDayF.format(parseLocal(s));
export const hour = (s: string) => hourF.format(parseLocal(s));
export const dayHour = (s: string) => `${weekday(s)} ${hour(s)}`;

export const pct = (v: number) => `${Math.round(v * 100)}%`;

function num(v: number): string {
	if (v >= 10) return String(Math.round(v));
	return String(Math.round(v * 10) / 10);
}

export function snowUnit(sys: System) {
	return sys === 'metric' ? 'cm' : 'in';
}
export function precipUnit(sys: System) {
	return sys === 'metric' ? 'mm' : 'in';
}

const snowVal = (cm: number, sys: System) => (sys === 'metric' ? cm : cm / 2.54);
const precipVal = (mm: number, sys: System) => (sys === 'metric' ? mm : mm / 25.4);

function precipNum(mm: number, sys: System): string {
	const v = precipVal(mm, sys);
	return sys === 'metric' ? num(v) : String(Math.round(v * 100) / 100);
}

/** "12–35 cm"; a single value when the range collapses. */
export function snowRange(q: Quantiles, sys: System): string {
	const lo = num(snowVal(q.p10, sys));
	const hi = num(snowVal(q.p90, sys));
	return lo === hi ? `${hi} ${snowUnit(sys)}` : `${lo}–${hi} ${snowUnit(sys)}`;
}

export function precipRange(q: Quantiles, sys: System): string {
	const lo = precipNum(q.p10, sys);
	const hi = precipNum(q.p90, sys);
	return lo === hi ? `${hi} ${precipUnit(sys)}` : `${lo}–${hi} ${precipUnit(sys)}`;
}

export const snowAmount = (cm: number, sys: System) => `${num(snowVal(cm, sys))} ${snowUnit(sys)}`;
export const precipAmount = (mm: number, sys: System) =>
	`${precipNum(mm, sys)} ${precipUnit(sys)}`;

export function elevation(m: number, sys: System): string {
	return sys === 'metric'
		? `${Math.round(m).toLocaleString()} m`
		: `${(Math.round((m * 3.28084) / 10) * 10).toLocaleString()} ft`;
}

export function temp(c: number, sys: System): string {
	return `${Math.round(sys === 'metric' ? c : (c * 9) / 5 + 32)}°`;
}

export const kindLabel: Record<Kind, string> = { snow: 'Snow', mixed: 'Mix', rain: 'Rain' };

const modelNames: Record<string, string> = {
	ecmwf_ifs025_ensemble: 'ECMWF',
	ncep_gefs025: 'GEFS',
	gem_global_ensemble: 'GEM',
	icon_eu_eps: 'ICON-EU'
};
export const modelName = (id: string) => modelNames[id] ?? id;
