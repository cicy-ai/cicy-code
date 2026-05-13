const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('fs')
const path = require('path')

const { makeStdin, makeTempHome, runCli } = require('./helpers')
const { main } = require('../google.js')

function cacheFile(home) {
  return path.join(home, '.cache', 'gmail-ids.json')
}

test('google without a service prints the service help', async () => {
  const result = await runCli(main, [], { lib: {} })
  assert.match(result.stdout, /Available services:/)
  assert.match(result.stdout, /gmail/)
  assert.match(result.stdout, /calendar/)
  assert.match(result.stdout, /google help \[service\]/)
})

test('google help supports top-level and service-specific help routes', async () => {
  let result = await runCli(main, ['help'], { lib: {} })
  assert.match(result.stdout, /Available services:/)

  result = await runCli(main, ['help', 'gmail'], { lib: {} })
  assert.match(result.stdout, /Usage: google gmail <list\|read\|read-all\|send\|watch>/)

  result = await runCli(main, ['gmail', 'help'], { lib: {} })
  assert.match(result.stdout, /Usage: google gmail <list\|read\|read-all\|send\|watch>/)
})

test('google gmail list and read use the Gmail command paths', async () => {
  const home = makeTempHome()
  const readCalls = []

  let result = await runCli(main, ['gmail', 'list', '1'], {
    env: { HOME: home },
    lib: {
      async gmailList(count) {
        assert.equal(count, 1)
        return [{ id: 'm1', date: '2026-04-05', from: 'a@example.com', subject: 'Alpha' }]
      },
    },
  })
  assert.match(result.stdout, /Alpha/)
  assert.deepEqual(JSON.parse(fs.readFileSync(cacheFile(home), 'utf8')), ['m1'])

  result = await runCli(main, ['gmail', 'read', '1'], {
    env: { HOME: home },
    lib: {
      async gmailRead(id) {
        readCalls.push(id)
        return {
          from: 'a@example.com',
          to: 'b@example.com',
          date: 'today',
          subject: 'Alpha',
          body: 'Body',
        }
      },
    },
  })
  assert.deepEqual(readCalls, ['m1'])
  assert.match(result.stdout, /Subject: Alpha/)
})

test('google gmail read-all, send, and watch execute all Gmail side-effect commands', async () => {
  const home = makeTempHome()
  fs.mkdirSync(path.join(home, '.cache'), { recursive: true })
  fs.writeFileSync(cacheFile(home), JSON.stringify(['m1', 'm2']))

  let markedIds
  let sentArgs
  let watchReads = 0

  let result = await runCli(main, ['gmail', 'read-all'], {
    env: { HOME: home },
    lib: {
      async gmailMarkRead(ids) {
        markedIds = ids
      },
    },
  })
  assert.deepEqual(markedIds, ['m1', 'm2'])
  assert.equal(result.stdout, 'Marked 2 emails as read.')

  result = await runCli(main, ['gmail', 'send', 'user@example.com', 'Subject'], {
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

  result = await runCli(main, ['gmail', 'watch', 'otp'], {
    lib: {
      async gmailListUnread() {
        return [{ id: 'm1', from: 'otp@example.com', subject: 'otp mail' }]
      },
      async gmailRead() {
        watchReads += 1
        return { from: 'otp@example.com', subject: 'otp mail', body: 'OTP 654321' }
      },
    },
    now: () => 0,
  })
  assert.equal(watchReads, 1)
  assert.match(result.stdout, /654321/)
})

test('google sheets commands cover list/read/write/append/create', async () => {
  const calls = []
  const lib = {
    async sheetsList() {
      calls.push('list')
      return [{ id: 'sheet-1', name: 'Budget', modifiedTime: '2026-04-05' }]
    },
    async sheetsRead(id, range) {
      calls.push(['read', id, range])
      return [['A', 'B']]
    },
    async sheetsWrite(id, range, values) {
      calls.push(['write', id, range, values])
    },
    async sheetsAppend(id, range, values) {
      calls.push(['append', id, range, values])
    },
    async sheetsCreate(title) {
      calls.push(['create', title])
      return { spreadsheetId: 'new-sheet' }
    },
  }

  let result = await runCli(main, ['sheets', 'list'], { lib })
  assert.match(result.stdout, /Budget/)

  result = await runCli(main, ['sheets', 'read', 'sheet-1', 'Sheet1!A1:B2'], { lib })
  assert.equal(result.stdout, 'A\tB')

  result = await runCli(main, ['sheets', 'write', 'sheet-1', 'Sheet1!A1', '[["Name"]]'], { lib })
  assert.equal(result.stdout, 'Written.')

  result = await runCli(main, ['sheets', 'append', 'sheet-1', 'Sheet1!A1', '[["Alice"]]'], { lib })
  assert.equal(result.stdout, 'Appended.')

  result = await runCli(main, ['sheets', 'create', 'New Sheet'], { lib })
  assert.equal(result.stdout, 'Created: new-sheet')

  assert.deepEqual(calls, [
    'list',
    ['read', 'sheet-1', 'Sheet1!A1:B2'],
    ['write', 'sheet-1', 'Sheet1!A1', [['Name']]],
    ['append', 'sheet-1', 'Sheet1!A1', [['Alice']]],
    ['create', 'New Sheet'],
  ])
})

test('google drive commands cover list/upload/download/upload-dir/download-dir/quota', async () => {
  const calls = []
  const lib = {
    async driveList(query, pageSize) {
      calls.push(['list', query, pageSize])
      return [{ id: 'file-1', name: 'doc.txt', mimeType: 'text/plain', size: '12' }]
    },
    async driveUpload(name, content) {
      calls.push(['upload', name, content])
      return { id: 'upload-1' }
    },
    async driveDownload(id) {
      calls.push(['download', id])
      return 'downloaded text'
    },
    async driveUploadDir(localPath, parentId, exclude) {
      calls.push(['upload-dir', localPath, parentId, exclude])
      return 'dir-1'
    },
    async driveDownloadDir(id, localPath) {
      calls.push(['download-dir', id, localPath])
    },
    async driveQuota() {
      calls.push(['quota'])
      return {
        usage: String(3 * 1024 ** 3),
        limit: String(10 * 1024 ** 3),
        usageInDrive: String(2 * 1024 ** 3),
        usageInDriveTrash: String(1 * 1024 ** 3),
      }
    },
  }

  let result = await runCli(main, ['drive', 'list', "name contains 'doc'", '5'], { lib })
  assert.match(result.stdout, /doc\.txt/)

  result = await runCli(main, ['drive', 'upload', 'doc.txt', 'hello'], { lib })
  assert.equal(result.stdout, 'Uploaded: upload-1')

  result = await runCli(main, ['drive', 'download', 'file-1'], { lib })
  assert.equal(result.stdout, 'downloaded text')

  result = await runCli(main, ['drive', 'upload-dir', '/tmp/source', '--exclude', 'node_modules,*.log'], { lib })
  assert.equal(result.stdout, 'Uploaded: dir-1')

  result = await runCli(main, ['drive', 'download-dir', 'dir-1', '/tmp/out'], { lib })
  assert.equal(result.stdout, 'Downloaded.')

  result = await runCli(main, ['drive', 'quota'], { lib })
  assert.match(result.stdout, /Used: 3\.00 GB \/ 10\.00 GB \(30\.00%\)/)
  assert.match(result.stdout, /Drive: 2\.00 GB/)
  assert.match(result.stdout, /Trash: 1\.00 GB/)

  assert.deepEqual(calls, [
    ['list', "name contains 'doc'", 5],
    ['upload', 'doc.txt', 'hello'],
    ['download', 'file-1'],
    ['upload-dir', '/tmp/source', null, ['node_modules', '*.log']],
    ['download-dir', 'dir-1', '/tmp/out'],
    ['quota'],
  ])
})

test('google calendar commands cover list/events/create', async () => {
  const calls = []
  const lib = {
    async calendarList() {
      calls.push('list')
      return [{ id: 'primary', summary: 'Primary' }]
    },
    async calendarEvents(calendarId, max) {
      calls.push(['events', calendarId, max])
      return [{ start: { dateTime: '2026-04-05T10:00:00Z' }, summary: 'Meeting' }]
    },
    async calendarCreate(calendarId, summary, start, end) {
      calls.push(['create', calendarId, summary, start, end])
      return { id: 'event-1' }
    },
  }

  let result = await runCli(main, ['calendar', 'list'], { lib })
  assert.match(result.stdout, /Primary/)

  result = await runCli(main, ['calendar', 'events', 'team', '3'], { lib })
  assert.equal(result.stdout, '2026-04-05T10:00:00Z  Meeting')

  result = await runCli(main, ['calendar', 'create', 'Sync', '2026-04-05T10:00:00Z', '2026-04-05T11:00:00Z'], { lib })
  assert.equal(result.stdout, 'Created: event-1')

  assert.deepEqual(calls, [
    'list',
    ['events', 'team', 3],
    ['create', 'primary', 'Sync', '2026-04-05T10:00:00Z', '2026-04-05T11:00:00Z'],
  ])
})

test('google prints service-specific usage when a command is missing', async t => {
  const cases = [
    { argv: ['gmail'], expected: 'Usage: google gmail <list|read|read-all|send|watch>' },
    { argv: ['sheets'], expected: 'Usage: google sheets <list|read|write|append|create>' },
    { argv: ['drive'], expected: 'Usage: google drive <list|upload|upload-dir|download|download-dir|quota>' },
    { argv: ['calendar'], expected: 'Usage: google calendar <list|events|create>' },
  ]

  for (const item of cases) {
    await t.test(item.argv[0], async () => {
      const result = await runCli(main, item.argv, { lib: {} })
      assert.match(result.stdout, new RegExp(item.expected.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    })
  }
})

test('google prints generic usage for unknown services', async () => {
  const result = await runCli(main, ['unknown'], { lib: {} })
  assert.match(result.stdout, /Available services:/)
})
