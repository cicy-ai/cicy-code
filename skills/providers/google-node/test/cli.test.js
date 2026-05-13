const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('fs')
const path = require('path')

const { makeStdin, makeTempHome, runCli } = require('./helpers')
const { main } = require('../cli.js')

function cacheFile(home) {
  return path.join(home, '.cache', 'gmail-ids.json')
}

test('gmail list formats results and saves ids to cache', async () => {
  const home = makeTempHome()
  const result = await runCli(main, ['list', '2'], {
    env: { HOME: home },
    lib: {
      async gmailList(count) {
        assert.equal(count, 2)
        return [
          { id: 'm1', date: '2026-04-05', from: 'a@example.com', subject: 'Alpha' },
          { id: 'm2', date: '2026-04-06', from: 'b@example.com', subject: 'Beta' },
        ]
      },
    },
  })

  assert.equal(result.exitCode, 0)
  assert.match(result.stdout, /1  2026-04-05  a@example\.com  Alpha/)
  assert.match(result.stdout, /2  2026-04-06  b@example\.com  Beta/)
  assert.deepEqual(JSON.parse(fs.readFileSync(cacheFile(home), 'utf8')), ['m1', 'm2'])
})

test('gmail read resolves cached numeric ids', async () => {
  const home = makeTempHome()
  fs.mkdirSync(path.join(home, '.cache'), { recursive: true })
  fs.writeFileSync(cacheFile(home), JSON.stringify(['cached-id']))

  let readId
  const result = await runCli(main, ['read', '1'], {
    env: { HOME: home },
    lib: {
      async gmailRead(id) {
        readId = id
        return {
          from: 'from@example.com',
          to: 'to@example.com',
          date: 'today',
          subject: 'Hello',
          body: 'message body',
        }
      },
    },
  })

  assert.equal(readId, 'cached-id')
  assert.match(result.stdout, /From: from@example\.com/)
  assert.match(result.stdout, /message body/)
})

test('gmail read-all marks cached messages as read', async () => {
  const home = makeTempHome()
  fs.mkdirSync(path.join(home, '.cache'), { recursive: true })
  fs.writeFileSync(cacheFile(home), JSON.stringify(['m1', 'm2']))

  let markedIds
  const result = await runCli(main, ['read-all'], {
    env: { HOME: home },
    lib: {
      async gmailMarkRead(ids) {
        markedIds = ids
      },
    },
  })

  assert.deepEqual(markedIds, ['m1', 'm2'])
  assert.equal(result.stdout, 'Marked 2 emails as read.')
})

test('gmail send reads the body from stdin when no body arg is provided', async () => {
  let sentArgs
  const result = await runCli(main, ['send', 'user@example.com', 'Subject'], {
    lib: {
      async gmailSend(to, subject, body) {
        sentArgs = { to, subject, body }
      },
    },
    stdin: makeStdin('stdin body', false),
  })

  assert.deepEqual(sentArgs, {
    to: 'user@example.com',
    subject: 'Subject',
    body: 'stdin body',
  })
  assert.equal(result.stdout, 'Sent.')
})

test('gmail watch prints a verification code when a matching unread mail appears', async () => {
  let listUnreadCount = 0
  const result = await runCli(main, ['watch', 'otp'], {
    lib: {
      async gmailListUnread() {
        listUnreadCount += 1
        return [{ id: 'm1', from: 'otp@example.com', subject: 'Your otp code' }]
      },
      async gmailRead(id) {
        assert.equal(id, 'm1')
        return { from: 'otp@example.com', subject: 'Your otp code', body: 'Code: 123456' }
      },
    },
    now: (() => {
      let current = 0
      return () => current
    })(),
  })

  assert.equal(listUnreadCount, 1)
  assert.match(result.stdout, /Watching for "otp"\.\.\. \(120s timeout\)/)
  assert.match(result.stdout, /123456/)
})

test('gmail watch exits with code 1 on timeout', async () => {
  let current = 0
  const result = await runCli(main, ['watch'], {
    lib: {
      async gmailListUnread() {
        return []
      },
      async gmailRead() {
        throw new Error('unexpected read')
      },
    },
    now: () => current,
    sleep: async ms => {
      current += ms
    },
  })

  assert.equal(result.exitCode, 1)
  assert.equal(result.stderr, 'Timeout, no matching email.')
})

test('gmail shows usage for unknown commands', async () => {
  const result = await runCli(main, ['unknown'], { lib: {} })
  assert.equal(result.stdout, 'Usage: gmail <list|read|read-all|send|watch>')
})
