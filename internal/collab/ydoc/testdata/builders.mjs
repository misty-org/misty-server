import * as Y from '../../../../../misty-sdk/node_modules/yjs/dist/yjs.mjs'
import { createHash } from 'node:crypto'

export const b64 = (bytes) => Buffer.from(bytes).toString('base64')

export const rng = (seed) => {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export const words = ['alpha', 'bravo', 'charlie', 'delta', 'echo', 'fox', 'golf', 'hotel', 'india', 'juliet', 'kilo', 'lima', 'mike', 'nov', 'oscar', 'papa', 'quebec', 'romeo', 'sierra', 'tango', 'umbrella', 'victor', 'whiskey', 'xray', 'yankee', 'zulu', 'é', 'ñandú', '日本', '😀', '👩‍👩‍👧', ' ', '\n']

export const makeDoc = (clientID, gc = true) => {
  const doc = new Y.Doc({ gc })
  doc.clientID = clientID
  return doc
}

export const sync = (docs) => {
  for (const a of docs) {
    for (const b of docs) {
      if (a === b) continue
      Y.applyUpdate(b, Y.encodeStateAsUpdate(a, Y.encodeStateVector(b)))
    }
  }
}

export const canon = (value) => {
  if (value instanceof Y.XmlText || value instanceof Y.XmlElement || value instanceof Y.XmlFragment) return JSON.stringify(value.toString())
  if (value instanceof Y.Text) return JSON.stringify(value.toString())
  if (value instanceof Y.Map) {
    const keys = [...value.keys()].sort()
    return '{' + keys.map((k) => JSON.stringify(k) + ':' + canon(value.get(k))).join(',') + '}'
  }
  if (value instanceof Y.Array) return '[' + value.toArray().map(canon).join(',') + ']'
  if (value instanceof Y.Doc) return 'null'
  if (value instanceof Uint8Array) return JSON.stringify(b64(value))
  if (typeof value === 'bigint') return value.toString()
  if (value === undefined || value === null) return 'null'
  if (Array.isArray(value)) return '[' + value.map(canon).join(',') + ']'
  if (typeof value === 'object') {
    const keys = Object.keys(value).sort()
    return '{' + keys.map((k) => JSON.stringify(k) + ':' + canon(value[k])).join(',') + '}'
  }
  return JSON.stringify(value)
}

export const canonDelta = (delta) => '[' + delta.map((op) => {
  const parts = ['"insert":' + (typeof op.insert === 'string' ? JSON.stringify(op.insert) : canon(op.insert))]
  if (op.attributes && Object.keys(op.attributes).length > 0) parts.push('"attributes":' + canon(op.attributes))
  return '{' + parts.join(',') + '}'
}).join(',') + ']'

export const rootJSON = (doc, roots) => {
  const out = {}
  for (const [name, kind] of Object.entries(roots)) {
    let text
    let empty
    if (kind === 'text') {
      const t = doc.getText(name)
      text = JSON.stringify(t.toString())
      empty = t.length === 0
    } else if (kind === 'xml') {
      const f = doc.getXmlFragment(name)
      text = JSON.stringify(f.toString())
      empty = f.length === 0
    } else if (kind === 'map') {
      const m = doc.getMap(name)
      text = canon(m)
      empty = m.size === 0
    } else {
      const a = doc.getArray(name)
      text = canon(a)
      empty = a.length === 0
    }
    out[name] = { kind, json: text, empty }
    if (kind === 'text') {
      const plain = doc.getText(name).toString()
      out[name].textSha256 = b64(createHash('sha256').update(plain, 'utf8').digest())
      out[name].length = doc.getText(name).length
      if (plain.length <= 50000) out[name].delta = canonDelta(doc.getText(name).toDelta())
      else out[name].json = undefined
    }
    if (text.length > 100000) out[name].json = undefined
  }
  return out
}

export const fingerprint = (doc) => b64(createHash('sha256').update(Y.encodeSnapshot(Y.snapshot(doc))).digest())

export const clientsOf = (update) => {
  const decoded = Y.decodeUpdate(update)
  const set = new Set(decoded.structs.map((s) => s.id.client))
  return [...set].sort((a, b) => a - b)
}

export const textBasic = (gc) => {
  const doc = makeDoc(1, gc)
  const t = doc.getText('body')
  t.insert(0, 'Hello world')
  t.insert(5, ',')
  t.delete(0, 1)
  t.insert(0, 'h')
  t.format(0, 5, { bold: true })
  t.insert(t.length, ' and more', { italic: true, color: '#ff0000' })
  t.delete(3, 2)
  t.insert(2, '😀👩‍👩‍👧 日本')
  t.insertEmbed(4, { image: 'x.png', width: 10 })
  return doc
}

export const textTwoClients = (gc) => {
  const a = makeDoc(11, gc)
  const b = makeDoc(12, gc)
  a.getText('t').insert(0, 'shared start')
  sync([a, b])
  a.getText('t').insert(6, ' from a')
  b.getText('t').insert(0, 'b says ')
  b.getText('t').delete(10, 3)
  a.getText('t').delete(0, 2)
  sync([a, b])
  b.getText('notes').insert(0, 'second root')
  sync([a, b])
  return a
}

export const mapBasic = (gc) => {
  const doc = makeDoc(21, gc)
  const m = doc.getMap('settings')
  m.set('title', 'first')
  m.set('title', 'second')
  m.set('count', 3)
  m.set('gone', 'soon')
  m.delete('gone')
  m.set('title', 'third')
  m.set('flag', true)
  doc.getMap('empty').set('only', 1)
  doc.getMap('empty').delete('only')
  return doc
}

export const arrayBasic = (gc) => {
  const doc = makeDoc(31, gc)
  const a = doc.getArray('items')
  a.insert(0, [1, 2, 3, 'four', { five: 5 }])
  a.delete(1, 2)
  a.insert(1, ['two again'])
  a.push([[1, [2, [3]]]])
  a.unshift([null])
  return doc
}

export const xmlDoc = (gc) => {
  const doc = makeDoc(41, gc)
  const f = doc.getXmlFragment('prosemirror')
  const p = new Y.XmlElement('paragraph')
  p.setAttribute('align', 'left')
  p.setAttribute('id', 'p1')
  const text = new Y.XmlText()
  p.insert(0, [text])
  text.insert(0, 'Some text')
  text.format(0, 4, { bold: {} })
  text.insert(9, ' linked', { link: { href: 'https://example.com' } })
  const h = new Y.XmlElement('heading')
  h.setAttribute('level', '2')
  const ht = new Y.XmlText()
  h.insert(0, [ht])
  ht.insert(0, 'Title')
  const list = new Y.XmlElement('bullet_list')
  const item = new Y.XmlElement('list_item')
  const ip = new Y.XmlElement('paragraph')
  const it = new Y.XmlText()
  ip.insert(0, [it])
  it.insert(0, 'nested item')
  item.insert(0, [ip])
  list.insert(0, [item])
  f.insert(0, [h, p, list])
  p.removeAttribute('id')
  ht.delete(0, 1)
  ht.insert(0, 't')
  return doc
}

export const values = () => [
  0, 1, -1, 63, 64, -64, -65, 127, 128, 2147483647, -2147483647, -2147483648, 2147483648, 4294967296,
  9007199254740991, -9007199254740991, 1e21, 0.1, 0.2, 1 / 3, 1e300, -1e300, 5e-324, 1.7976931348623157e308,
  0.5, -2.25, 1024.75, Math.fround(1.1), Math.fround(3.14159), 1e-7, 123456789.125,
  true, false, null, '', 'plain', 'emoji 😀 and 👩‍👩‍👧 and 𝄞', 'quote " backslash \\ newline \n tab \t', '日本語', '<b>&amp;</b>',
  { a: 1, nested: { b: [1, 2, { c: 'd' }], e: null }, z: 0.1 },
  { zebra: 1, apple: 2, mango: { y: 1, b: 2 } },
  [1, 'two', [3, [4]], { five: 5 }],
  [],
  {},
  new Uint8Array([0, 1, 2, 254, 255]),
  new Uint8Array([]),
  { bytes: new Uint8Array([9, 8, 7]), big: 1234567890123456789n, small: -5n, max: 9223372036854775807n, min: -9223372036854775808n },
  [new Uint8Array([1]), 42n]
]

export const valueDoc = (gc) => {
  const doc = makeDoc(51, gc)
  const m = doc.getMap('values')
  const list = values()
  list.forEach((v, i) => m.set(`v${String(i).padStart(2, '0')}`, v))
  doc.getArray('list').insert(0, list)
  list.forEach((v) => doc.getArray('singles').push([v]))
  const t = doc.getText('embeds')
  t.insert(0, 'ab')
  t.insertEmbed(1, { num: 0.1, big: 1e300, f: Math.fround(1.1), arr: [1, 2], s: '😀' })
  t.insert(1, 'x', { size: 0.1, weight: 700, on: true, nested: { a: [1] } })
  return doc
}

export const nestedDoc = (gc) => {
  const doc = makeDoc(61, gc)
  const root = doc.getMap('state')
  const inner = new Y.Map()
  root.set('inner', inner)
  inner.set('name', 'inner map')
  inner.set('n', 0.1)
  const arr = new Y.Array()
  inner.set('arr', arr)
  arr.insert(0, [1, 2, 3])
  const deepText = new Y.Text()
  arr.push([deepText])
  deepText.insert(0, 'deep text')
  const text = new Y.Text()
  root.set('text', text)
  text.insert(0, 'map text')
  const xml = new Y.XmlFragment()
  root.set('xml', xml)
  const el = new Y.XmlElement('p')
  xml.insert(0, [el])
  const xt = new Y.XmlText()
  el.insert(0, [xt])
  xt.insert(0, 'in xml')
  root.set('plain', { a: [1, 2] })
  const list = doc.getArray('list')
  const m1 = new Y.Map()
  list.insert(0, [m1, 'between', new Y.Array()])
  m1.set('k', 'v')
  list.get(2).insert(0, [new Y.Map()])
  list.get(2).get(0).set('deep', true)
  return doc
}

export const manyClients = (gc) => {
  const docs = []
  for (let i = 0; i < 24; i++) docs.push(makeDoc(1000 + i * 7, gc))
  const r = rng(7)
  for (let round = 0; round < 6; round++) {
    for (const d of docs) {
      const t = d.getText('text')
      const pos = Math.floor(r() * (t.length + 1))
      t.insert(pos, words[Math.floor(r() * words.length)])
      if (t.length > 10 && r() < 0.4) t.delete(Math.floor(r() * (t.length - 3)), 2)
      d.getMap('who').set(`k${Math.floor(r() * 8)}`, d.clientID)
      const a = d.getArray('log')
      a.insert(Math.floor(r() * (a.length + 1)), [round, d.clientID])
      if (a.length > 4 && r() < 0.3) a.delete(Math.floor(r() * (a.length - 1)), 1)
    }
    sync(docs)
  }
  return docs[0]
}

export const heavy = (seed, clients, ops, gc) => {
  const docs = []
  for (let i = 0; i < clients; i++) docs.push(makeDoc(seed * 100 + i + 1, gc))
  const r = rng(seed)
  for (let i = 0; i < ops; i++) {
    const d = docs[Math.floor(r() * docs.length)]
    const kind = r()
    const t = d.getText('text')
    if (kind < 0.5) {
      const n = 1 + Math.floor(r() * 4)
      let s = ''
      for (let w = 0; w < n; w++) s += words[Math.floor(r() * words.length)]
      t.insert(Math.floor(r() * (t.length + 1)), s, r() < 0.1 ? { bold: true } : undefined)
    } else if (kind < 0.7) {
      if (t.length > 0) {
        const pos = Math.floor(r() * t.length)
        t.delete(pos, Math.min(t.length - pos, 1 + Math.floor(r() * 20)))
      }
    } else if (kind < 0.85) {
      const m = d.getMap('map')
      const key = `key${Math.floor(r() * 64)}`
      if (r() < 0.15) m.delete(key)
      else m.set(key, r() < 0.5 ? Math.floor(r() * 1e6) : r() < 0.5 ? r() : { at: i, w: words[Math.floor(r() * words.length)] })
    } else {
      const a = d.getArray('array')
      if (a.length > 0 && r() < 0.4) {
        const pos = Math.floor(r() * a.length)
        a.delete(pos, Math.min(a.length - pos, 1 + Math.floor(r() * 3)))
      } else {
        a.insert(Math.floor(r() * (a.length + 1)), [i, words[Math.floor(r() * words.length)]])
      }
    }
    if (i % 61 === 0) sync(docs)
  }
  sync(docs)
  return { doc: docs[0], ops }
}

export const bigDoc = (seed, clients, target) => {
  const docs = []
  for (let i = 0; i < clients; i++) docs.push(makeDoc(seed * 100 + i + 1, true))
  const r = rng(seed)
  let i = 0
  let near = false
  for (; ; i++) {
    if (i > 0 && (i % 2000 === 0 || (near && i % 50 === 0))) {
      sync(docs)
      const size = Y.encodeStateAsUpdate(docs[0]).length
      if (size >= target) break
      near = size >= target - 600 * 1024
    }
    const d = docs[Math.floor(r() * docs.length)]
    const kind = r()
    const t = d.getText('text')
    if (kind < 0.8) {
      const n = 20 + Math.floor(r() * 200)
      let s = ''
      for (let w = 0; w < n; w++) s += words[Math.floor(r() * words.length)]
      t.insert(Math.floor(r() * (t.length + 1)), s)
    } else if (kind < 0.9) {
      if (t.length > 0) {
        const pos = Math.floor(r() * t.length)
        t.delete(pos, Math.min(t.length - pos, 1 + Math.floor(r() * 300)))
      }
    } else if (kind < 0.97) {
      d.getMap('map').set(`key${Math.floor(r() * 64)}`, { at: i, v: r() })
    } else {
      const a = d.getArray('array')
      if (a.length > 0 && r() < 0.4) a.delete(Math.floor(r() * a.length), 1)
      else a.insert(Math.floor(r() * (a.length + 1)), [i])
    }
    if (i % 397 === 0) sync(docs)
  }
  sync(docs)
  return { doc: docs[0], ops: i }
}
