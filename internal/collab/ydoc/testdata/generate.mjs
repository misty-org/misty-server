import * as Y from '../../../../../misty-sdk/node_modules/yjs/dist/yjs.mjs'
import { createHash } from 'node:crypto'
import { writeFileSync, readdirSync, unlinkSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { b64, rng, words, makeDoc, sync, rootJSON, fingerprint, clientsOf, textBasic, textTwoClients, mapBasic, arrayBasic, xmlDoc, valueDoc, nestedDoc, manyClients, heavy, bigDoc } from './builders.mjs'

const here = dirname(fileURLToPath(import.meta.url))

for (const file of readdirSync(here)) {
  if (file.endsWith('.bin') || file.endsWith('.bin.gz') || file.endsWith('.json')) unlinkSync(join(here, file))
}

const cases = []
const writeBin = (name, bytes) => {
  writeFileSync(join(here, name), bytes)
  return name
}

const record = (name, doc, roots, extra = {}) => {
  const update = Y.encodeStateAsUpdate(doc)
  const file = writeBin(`${name}.bin`, update)
  const reloaded = makeDoc(999999, true)
  Y.applyUpdate(reloaded, update)
  const entry = {
    name,
    file,
    gc: doc.gc,
    roots: rootJSON(doc, roots),
    stateVector: b64(Y.encodeStateVector(doc)),
    stateVectorFromUpdate: b64(Y.encodeStateVectorFromUpdate(update)),
    snapshot: b64(Y.encodeSnapshot(Y.snapshot(doc))),
    fingerprint: fingerprint(doc),
    clients: clientsOf(update),
    size: update.length,
    gcReload: doc.gc ? undefined : b64(Y.encodeStateAsUpdate(reloaded)),
    gcReloadFingerprint: doc.gc ? undefined : fingerprint(reloaded),
    ...extra
  }
  cases.push(entry)
  return update
}

const standardRoots = {
  body: 'text'
}

for (const gc of [true, false]) {
  const suffix = gc ? 'gc' : 'nogc'
  record(`text_basic_${suffix}`, textBasic(gc), standardRoots)
  record(`text_two_clients_${suffix}`, textTwoClients(gc), { t: 'text', notes: 'text' })
  record(`map_basic_${suffix}`, mapBasic(gc), { settings: 'map', empty: 'map' })
  record(`array_basic_${suffix}`, arrayBasic(gc), { items: 'array' })
  record(`xml_${suffix}`, xmlDoc(gc), { prosemirror: 'xml' })
  record(`values_${suffix}`, valueDoc(gc), { values: 'map', list: 'array', singles: 'array', embeds: 'text' })
  record(`nested_${suffix}`, nestedDoc(gc), { state: 'map', list: 'array' })
  record(`many_clients_${suffix}`, manyClients(gc), { text: 'text', who: 'map', log: 'array' })
  const h = heavy(gc ? 3 : 4, 4, 20000, gc)
  record(`heavy_${suffix}`, h.doc, { text: 'text', map: 'map', array: 'array' }, { ops: h.ops })
}

const zero = makeDoc(0, true)
zero.getText('t').insert(0, 'from client zero')
zero.getMap('m').set('a', 1)
record('client_zero', zero, { t: 'text', m: 'map' })

const zeroMixed = makeDoc(0, true)
const other = makeDoc(77, true)
zeroMixed.getText('t').insert(0, 'zero ')
sync([zeroMixed, other])
other.getText('t').insert(5, 'seventy seven')
sync([zeroMixed, other])
record('client_zero_mixed', zeroMixed, { t: 'text' })

const written = makeDoc(71, true)
written.transact(() => {
  const m = written.getMap('m')
  m.set('float', 0.1)
  m.set('huge', 1e300)
  m.set('half', 1.5)
  m.set('int', 7)
  m.set('neg', -300)
  m.set('pow40', 2 ** 40)
  m.set('str', 'hé 😀')
  m.set('bool', false)
  m.set('nil', null)
  m.set('list', [1, 'two', 0.1])
  m.set('obj', { a: 1, b: [true] })
  m.set('int', 8)
  const t = written.getText('t')
  t.insert(0, 'hello world')
  t.delete(0, 6)
  written.getArray('a').insert(0, [1, 0.25, 'x'])
})
record('written', written, { m: 'map', t: 'text', a: 'array' })

const empty = makeDoc(5, true)
record('empty', empty, {})

const big = bigDoc(9, 3, 8 * 1024 * 1024 - 64 * 1024)
const bigUpdate = Y.encodeStateAsUpdate(big.doc)
writeFileSync(join(here, 'big.bin.gz'), gzipSync(bigUpdate, { level: 9 }))
cases.push({
  name: 'big',
  file: 'big.bin.gz',
  gzip: true,
  gc: true,
  ops: big.ops,
  roots: rootJSON(big.doc, { text: 'text', map: 'map', array: 'array' }),
  stateVector: b64(Y.encodeStateVector(big.doc)),
  stateVectorFromUpdate: b64(Y.encodeStateVectorFromUpdate(bigUpdate)),
  snapshot: b64(Y.encodeSnapshot(Y.snapshot(big.doc))),
  fingerprint: fingerprint(big.doc),
  clients: clientsOf(bigUpdate),
  size: bigUpdate.length,
  sha256: b64(createHash('sha256').update(bigUpdate).digest())
})

writeFileSync(join(here, 'cases.json'), JSON.stringify(cases, null, 1))

const syncCases = []
const syncCase = (name, gc, build) => {
  const docs = build(gc)
  const updates = []
  const listeners = docs.map((d) => {
    const fn = (u) => updates.push(u)
    d.on('update', fn)
    return fn
  })
  const steps = docs.steps
  let midState = null
  let midSV = null
  steps.forEach((step, index) => {
    step()
    if (index === Math.floor(steps.length / 2)) {
      sync(docs)
      midState = Y.encodeStateAsUpdate(docs[0])
      midSV = Y.encodeStateVector(docs[0])
    }
  })
  sync(docs)
  docs.forEach((d, i) => d.off('update', listeners[i]))
  const final = Y.encodeStateAsUpdate(docs[0])
  const merged = Y.mergeUpdates(updates)
  const diff = Y.diffUpdate(final, midSV)
  const mergedDoc = makeDoc(424242, gc)
  Y.applyUpdate(mergedDoc, merged)
  const roots = docs.roots
  syncCases.push({
    name,
    gc,
    updates: updates.map(b64),
    merged: b64(merged),
    mergedStateVector: b64(Y.encodeStateVectorFromUpdate(merged)),
    final: b64(final),
    finalStateVector: b64(Y.encodeStateVector(docs[0])),
    mid: b64(midState),
    midStateVector: b64(midSV),
    diff: b64(diff),
    roots: rootJSON(docs[0], roots),
    mergedRoots: rootJSON(mergedDoc, roots),
    fingerprint: fingerprint(docs[0])
  })
}

const syncBuild = (gc) => {
  const docs = [makeDoc(201, gc), makeDoc(202, gc), makeDoc(203, gc)]
  const r = rng(201)
  docs.roots = { text: 'text', map: 'map', array: 'array' }
  docs.steps = []
  for (let i = 0; i < 60; i++) {
    docs.steps.push(() => {
      const d = docs[Math.floor(r() * 3)]
      const t = d.getText('text')
      t.insert(Math.floor(r() * (t.length + 1)), words[Math.floor(r() * words.length)])
      if (r() < 0.3 && t.length > 4) t.delete(Math.floor(r() * (t.length - 2)), 2)
      d.getMap('map').set(`k${Math.floor(r() * 5)}`, r() < 0.5 ? 0.1 * i : 'v' + i)
      const a = d.getArray('array')
      a.insert(Math.floor(r() * (a.length + 1)), [i])
      if (i % 9 === 0) sync(docs)
    })
  }
  return docs
}

syncCase('sync_gc', true, syncBuild)
syncCase('sync_nogc', false, syncBuild)
writeFileSync(join(here, 'sync.json'), JSON.stringify(syncCases, null, 1))

const restoreCases = []
const restoreCase = (name, gc, roots, buildA, editB) => {
  const doc = makeDoc(301, gc)
  buildA(doc)
  const updateA = Y.encodeStateAsUpdate(doc)
  const jsonA = rootJSON(doc, roots)
  const editor = makeDoc(302, gc)
  Y.applyUpdate(editor, updateA)
  editB(editor, doc)
  sync([doc, editor])
  const updateB = Y.encodeStateAsUpdate(doc)
  restoreCases.push({
    name,
    gc,
    a: writeBin(`restore_${name}_a.bin`, updateA),
    b: writeBin(`restore_${name}_b.bin`, updateB),
    rootsA: jsonA,
    rootsB: rootJSON(doc, roots)
  })
}

for (const gc of [true, false]) {
  const s = gc ? 'gc' : 'nogc'
  restoreCase(`text_${s}`, gc, { body: 'text', title: 'text', extra: 'text' }, (d) => {
    d.getText('body').insert(0, 'Version A body with 😀 emoji')
    d.getText('body').format(0, 7, { bold: true })
    d.getText('title').insert(0, 'Title A')
  }, (d) => {
    d.getText('body').delete(0, 8)
    d.getText('body').insert(0, 'B rewrote ')
    d.getText('title').delete(0, d.getText('title').length)
    d.getText('extra').insert(0, 'only in B')
  })
  restoreCase(`map_${s}`, gc, { state: 'map', meta: 'map' }, (d) => {
    const m = d.getMap('state')
    m.set('title', 'A')
    m.set('n', 0.1)
    m.set('big', 1e300)
    m.set('obj', { a: [1, 2, { b: 'c' }] })
    m.set('removed', 'in A only')
    m.set('bytes', new Uint8Array([1, 2, 3]))
    m.set('bytesGone', new Uint8Array([4]))
    const inner = new Y.Map()
    m.set('inner', inner)
    inner.set('x', 1)
    const deep = new Y.Map()
    inner.set('deep', deep)
    deep.set('y', 'z')
    const arr = new Y.Array()
    m.set('arr', arr)
    arr.insert(0, [1, 'two', { three: 3 }])
    const txt = new Y.Text()
    m.set('text', txt)
    txt.insert(0, 'map text A')
    d.getMap('meta').set('v', 1)
  }, (d) => {
    const m = d.getMap('state')
    m.set('title', 'B')
    m.delete('removed')
    m.delete('bytesGone')
    m.set('bytes', new Uint8Array([9]))
    m.set('added', 'in B only')
    m.get('inner').set('x', 2)
    m.get('inner').get('deep').set('y', 'changed')
    m.get('arr').delete(0, 1)
    m.get('arr').push(['four'])
    m.get('text').insert(0, 'B: ')
    d.getMap('meta').set('v', 2)
  })
  restoreCase(`array_${s}`, gc, { list: 'array' }, (d) => {
    const a = d.getArray('list')
    a.insert(0, [1, 0.1, 'three', { four: 4 }, [5], new Uint8Array([7, 7]), Math.fround(1.1)])
    const m = new Y.Map()
    a.push([m])
    m.set('k', 'v')
    const inner = new Y.Array()
    a.push([inner])
    inner.insert(0, ['x', 'y'])
    const t = new Y.Text()
    a.push([t])
    t.insert(0, 'array text')
  }, (d) => {
    const a = d.getArray('list')
    a.delete(0, 2)
    a.insert(0, ['new first'])
    a.get(6).set('k', 'changed')
    a.push(['tail'])
  })
  restoreCase(`xml_${s}`, gc, { doc: 'xml' }, (d) => {
    const f = d.getXmlFragment('doc')
    const h = new Y.XmlElement('heading')
    h.setAttribute('level', '1')
    const ht = new Y.XmlText()
    h.insert(0, [ht])
    ht.insert(0, 'Heading A')
    const p = new Y.XmlElement('paragraph')
    p.setAttribute('class', 'lead')
    const pt = new Y.XmlText()
    p.insert(0, [pt])
    pt.insert(0, 'Paragraph A text')
    pt.format(0, 9, { strong: {} })
    const list = new Y.XmlElement('list')
    const li = new Y.XmlElement('item')
    const lt = new Y.XmlText()
    li.insert(0, [lt])
    lt.insert(0, 'nested')
    list.insert(0, [li])
    f.insert(0, [h, p, list])
  }, (d) => {
    const f = d.getXmlFragment('doc')
    f.delete(0, 1)
    const p = f.get(0)
    p.setAttribute('class', 'changed')
    p.get(0).insert(0, 'B ')
    const extra = new Y.XmlElement('paragraph')
    f.insert(2, [extra])
  })
  restoreCase(`mixed_${s}`, gc, { text: 'text', map: 'map', list: 'array', xml: 'xml' }, (d) => {
    d.getText('text').insert(0, 'text A')
    d.getMap('map').set('a', 1)
    d.getArray('list').insert(0, ['a'])
    const e = new Y.XmlElement('p')
    d.getXmlFragment('xml').insert(0, [e])
    e.insert(0, [new Y.XmlText('xml A')])
  }, (d) => {
    d.getText('text').insert(6, ' then B')
    d.getMap('map').set('b', 2)
    d.getArray('list').push(['b'])
    d.getXmlFragment('xml').get(0).get(0).insert(0, 'B ')
  })
}
restoreCase('emptied_gc', true, { body: 'text', state: 'map' }, (d) => {
  d.getText('body').insert(0, 'was here')
  d.getMap('state').set('k', 'v')
}, (d) => {
  d.getText('body').delete(0, d.getText('body').length)
  d.getMap('state').delete('k')
})
writeFileSync(join(here, 'restore.json'), JSON.stringify(restoreCases, null, 1))

console.log(`cases ${cases.length}, sync ${syncCases.length}, restore ${restoreCases.length}, big ${bigUpdate.length} bytes after ${big.ops} ops`)
