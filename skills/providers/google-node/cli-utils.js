const fs = require('fs')
const { resolve } = require('path')

function cachePath(env = process.env, resolvePath = resolve) {
  return resolvePath(env.HOME, '.cache/gmail-ids.json')
}

function saveIds(mails, { fsImpl = fs, env = process.env, resolvePath = resolve } = {}) {
  fsImpl.mkdirSync(resolvePath(env.HOME, '.cache'), { recursive: true })
  fsImpl.writeFileSync(cachePath(env, resolvePath), JSON.stringify(mails.map(m => m.id)))
}

function loadIds({ fsImpl = fs, env = process.env, resolvePath = resolve } = {}) {
  try {
    return JSON.parse(fsImpl.readFileSync(cachePath(env, resolvePath), 'utf8'))
  } catch {
    return []
  }
}

function resolveId(arg, deps = {}) {
  const n = Number(arg)
  if (!Number.isNaN(n) && n >= 1 && n <= 100) {
    const ids = loadIds(deps)
    if (ids[n - 1]) {
      return ids[n - 1]
    }
  }
  return arg
}

async function readStdin(stdin) {
  const chunks = []
  for await (const chunk of stdin) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  }
  return Buffer.concat(chunks).toString()
}

function watchBanner(keyword, timeoutSeconds) {
  return `Watching for ${keyword ? '"' + keyword + '"' : 'new emails'}... (${timeoutSeconds}s timeout)`
}

async function watchForMatchingMail({
  keyword = '',
  listUnread,
  read,
  print,
  error,
  sleep,
  now,
  timeoutSeconds = 120,
  intervalSeconds = 3,
}) {
  const seen = new Set()
  print(watchBanner(keyword, timeoutSeconds))

  const start = now()
  while (now() - start < timeoutSeconds * 1000) {
    const mails = await listUnread(5)
    for (const mail of mails || []) {
      if (seen.has(mail.id)) {
        continue
      }
      seen.add(mail.id)

      const lowerKeyword = keyword.toLowerCase()
      if (
        keyword &&
        !mail.subject?.toLowerCase().includes(lowerKeyword) &&
        !mail.from?.toLowerCase().includes(lowerKeyword)
      ) {
        continue
      }

      const full = await read(mail.id)
      const code = full.body?.match(/\b(\d{4,8})\b/)?.[1]
      if (code) {
        print(code)
      } else {
        print(`From: ${full.from}\nSubject: ${full.subject}\n\n${full.body}`)
      }
      return true
    }

    await sleep(intervalSeconds * 1000)
  }

  error('Timeout, no matching email.')
  return false
}

module.exports = {
  cachePath,
  loadIds,
  readStdin,
  resolveId,
  saveIds,
  watchBanner,
  watchForMatchingMail,
}
