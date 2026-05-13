#!/usr/bin/env node

// Gmail CLI Tool
// Usage:
//   gmail list [count]          - List recent emails
//   gmail read <n>              - Read email by index
//   gmail read-all               - Mark all listed emails as read
//   gmail send <to> <subject>   - Send email (body from stdin)

const fs = require('fs')
const { loadIds, readStdin, resolveId, saveIds, watchForMatchingMail } = require('./cli-utils')

function defaultSleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function main(argv = process.argv.slice(2), deps = {}) {
  const lib = deps.lib || require('./lib')
  const env = deps.env || process.env
  const fsImpl = deps.fs || fs
  const stdin = deps.stdin || process.stdin
  const print = deps.print || console.log
  const error = deps.error || console.error
  const exit = deps.exit || (code => process.exit(code))
  const sleep = deps.sleep || defaultSleep
  const now = deps.now || Date.now

  const [cmd, ...args] = argv
  const cacheDeps = { env, fsImpl }

  switch (cmd) {
    case 'list': {
      const mails = await lib.gmailList(Number(args[0]) || 10)
      saveIds(mails, cacheDeps)
      mails.forEach((m, i) => print(`${i + 1}  ${m.date}  ${m.from}  ${m.subject}`))
      break
    }
    case 'read': {
      if (!args[0]) { error('Usage: gmail read <n>'); exit(1); return }
      const m = await lib.gmailRead(resolveId(args[0], cacheDeps))
      print(`From: ${m.from}\nTo: ${m.to}\nDate: ${m.date}\nSubject: ${m.subject}\n\n${m.body}`)
      break
    }
    case 'read-all': {
      const ids = loadIds(cacheDeps)
      if (!ids.length) { error('Run "gmail list" first'); exit(1); return }
      await lib.gmailMarkRead(ids)
      print(`Marked ${ids.length} emails as read.`)
      break
    }
    case 'send': {
      if (args.length < 2) { error('Usage: gmail send <to> <subject> [body]'); exit(1); return }
      let body = args[2] || ''
      if (!body && !stdin.isTTY) {
        body = await readStdin(stdin)
      }
      await lib.gmailSend(args[0], args[1], body)
      print('Sent.')
      break
    }
    case 'watch': {
      const matched = await watchForMatchingMail({
        keyword: args[0] || '',
        listUnread: lib.gmailListUnread,
        read: lib.gmailRead,
        print,
        error,
        sleep,
        now,
      })
      if (!matched) {
        exit(1)
      }
      break
    }
    default:
      print('Usage: gmail <list|read|read-all|send|watch>')
  }
}

if (require.main === module) {
  main().catch(e => { console.error(e.message); process.exit(1) })
}

module.exports = {
  main,
}
