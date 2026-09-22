import { describe, expect, it } from 'vitest'
import {
  isStraightSequence,
  msg,
  passwordChecklist,
  validateConfirm,
  validateHttpUrl,
  validateJira,
  validatePassword,
  validateUsername,
} from './validation'

describe('validatePassword', () => {
  const cases: { name: string; pw: string; username?: string; want: string | null }[] = [
    { name: 'empty', pw: '', want: '请输入密码' },
    { name: 'short counts code points', pw: 'Ab3$', want: '密码至少 12 位（当前 4 位）' },
    { name: 'CJK counted as code points', pw: '潮汐发布控制台的密码', want: '密码至少 12 位（当前 10 位）' },
    { name: 'emoji counted as one each', pw: '🌊🌊🌊🌊🌊🌊🌊🌊🌊🌊🌊', want: '密码至少 12 位（当前 11 位）' },
    { name: 'over 72 bytes', pw: '潮'.repeat(25), want: '密码不能超过 72 字节' },
    { name: 'exactly 72 bytes ok', pw: '潮汐发布控制台密码安全吗好的是吧嗯哦哈呀嘿啊呢吧'.slice(0, 24), want: null },
    { name: 'all whitespace', pw: '            ', want: '密码不能全是空白' },
    { name: 'leading space', pw: ' Tide-Release-9', want: '密码首尾不能有空格' },
    { name: 'trailing space', pw: 'Tide-Release-9 ', want: '密码首尾不能有空格' },
    { name: 'inner space ok', pw: 'tide release nine', want: null },
    { name: 'fewer than 4 distinct', pw: 'abcabcabcabc', want: '密码过于简单：至少包含 4 个不同的字符' },
    { name: 'repeated char', pw: 'aaaaaaaaaaaa', want: '密码过于简单：至少包含 4 个不同的字符' },
    { name: 'ascending letters', pw: 'abcdefghijkl', want: '密码过于简单：不能是连续的字符序列' },
    { name: 'ascending letters mixed case', pw: 'AbCdEfGhIjKl', want: '密码过于简单：不能是连续的字符序列' },
    { name: 'descending letters', pw: 'zyxwvutsrqpo', want: '密码过于简单：不能是连续的字符序列' },
    { name: 'ascending digits wrap 9→0', pw: '123456789012', want: '密码过于简单：不能是连续的字符序列' },
    { name: 'descending digits wrap 0→9', pw: '987654321098', want: '密码过于简单：不能是连续的字符序列' },
    { name: 'common list', pw: 'password1234', want: '这是常见弱密码，请换一个' },
    { name: 'common list case-insensitive', pw: 'PassW0rd1234', want: '这是常见弱密码，请换一个' },
    { name: 'common list p@ssw0rd1234', pw: 'P@ssw0rd1234', want: '这是常见弱密码，请换一个' },
    { name: 'common list tide12345678', pw: 'tide12345678', want: '这是常见弱密码，请换一个' },
    { name: 'contains username', pw: 'my-Alice-release-key', username: 'alice', want: '密码不能包含用户名' },
    { name: 'short username ignored', pw: 'xx-release-key-9', username: 'xx', want: null },
    { name: 'good password', pw: 'correct horse battery', username: 'admin', want: null },
  ]
  it.each(cases)('$name', ({ pw, username, want }) => {
    expect(validatePassword(pw, username)).toBe(want)
  })
})

describe('isStraightSequence', () => {
  it.each([
    ['abcdefghijkl', true],
    ['123456789012', true],
    ['987654321098', true],
    ['890123456789', true],
    ['abcdefghijkm', false],
    ['1234567890ab', false],
    ['a', false],
  ] as const)('%s → %s', (s, want) => {
    expect(isStraightSequence(s)).toBe(want)
  })
})

describe('passwordChecklist', () => {
  it('marks rules as the password improves', () => {
    const short = passwordChecklist('abc', 'admin')
    expect(short.find((r) => r.key === 'length')?.ok).toBe(false)
    const good = passwordChecklist('correct horse battery', 'admin')
    expect(good.every((r) => r.ok)).toBe(true)
  })
})

describe('validateConfirm', () => {
  it.each([
    ['Tide-Release-9', '', '请再次输入密码'],
    ['Tide-Release-9', 'Tide-Release-8', '两次输入的密码不一致'],
    ['Tide-Release-9', 'Tide-Release-9', null],
  ] as const)('%s / %s', (pw, confirm, want) => {
    expect(validateConfirm(pw, confirm)).toBe(want)
  })
})

describe('validateUsername', () => {
  const bad = '用户名需以小写字母开头，2–32 位，只能包含小写字母、数字和 . _ -'
  it.each([
    ['', '请输入用户名'],
    ['a', bad],
    ['ab', null],
    ['admin', null],
    ['ops.lead_2-b', null],
    ['Admin', bad],
    ['1admin', bad],
    ['_admin', bad],
    ['ad min', bad],
    ['a'.repeat(32), null],
    ['a'.repeat(33), bad],
    ['zhang@corp', bad],
  ] as const)('%s', (u, want) => {
    expect(validateUsername(u)).toBe(want)
  })
})

describe('validateJira', () => {
  const bad = 'Jira 单号格式应为 项目-数字，例如 OPS-1234'
  it.each([
    ['', '请输入 Jira 单号'],
    ['OPS-1234', null],
    ['ops-1234', null],
    [' OPS-1 ', null],
    ['AB_2-9', null],
    ['O-1', bad],
    ['OPS1234', bad],
    ['OPS-', bad],
    ['1OPS-12', bad],
    ['OPS-12a', bad],
  ] as const)('%s', (v, want) => {
    expect(validateJira(v)).toBe(want)
  })
})

describe('validateHttpUrl', () => {
  it.each([
    ['', {}, '请输入地址'],
    ['https://kargo.example.com', { base: true }, null],
    ['http://10.0.0.1:8080/api', { base: true }, null],
    ['kargo.example.com', {}, '请输入完整的 http(s) 地址，例如 https://example.com'],
    ['ftp://example.com', {}, '请输入完整的 http(s) 地址，例如 https://example.com'],
    ['javascript:alert(1)', {}, '请输入完整的 http(s) 地址，例如 https://example.com'],
    ['https://user:pass@example.com', {}, msg.urlCredentials()],
    ['https://example.com/?a=1', { base: true }, '基础地址不能带 ? 查询参数或 # 片段'],
    ['https://example.com/#x', { base: true }, '基础地址不能带 ? 查询参数或 # 片段'],
    ['https://grafana/d/x?var-service={service}', {}, null],
    ['https://tide.example.com/api/v1/auth/sso/callback', { base: true, suffix: '/api/v1/auth/sso/callback' }, null],
    ['https://tide.example.com/auth/callback', { base: true, suffix: '/api/v1/auth/sso/callback' }, '地址必须以 /api/v1/auth/sso/callback 结尾'],
  ] as const)('%s %o', (v, opts, want) => {
    expect(validateHttpUrl(v, opts)).toBe(want)
  })
})
