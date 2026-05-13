const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('fs')
const os = require('os')
const path = require('path')
const { PassThrough } = require('stream')

const { createGoogleapisMock, loadFresh, makeTempHome } = require('./helpers')

const libPath = path.join(__dirname, '..', 'lib.js')

function streamWith(text) {
  const stream = new PassThrough()
  process.nextTick(() => stream.end(text))
  return stream
}

test('gmailList maps metadata and uses web OAuth credentials when present', async () => {
  const gmailClient = {
    users: {
      messages: {
        async list(args) {
          assert.deepEqual(args, { userId: 'me', maxResults: 2 })
          return { data: { messages: [{ id: 'm1' }, { id: 'm2' }] } }
        },
        async get(args) {
          return {
            data: {
              payload: {
                headers: [
                  { name: 'From', value: `sender-${args.id}@example.com` },
                  { name: 'Subject', value: `subject-${args.id}` },
                  { name: 'Date', value: `date-${args.id}` },
                ],
              },
            },
          }
        },
      },
    },
  }
  const googleapis = createGoogleapisMock({ gmail: gmailClient })
  const home = makeTempHome({
    GMAIL_WEB_CLIENT_ID: 'web-client-id',
    GMAIL_WEB_CLIENT_SECRET: 'web-client-secret',
  })

  const lib = loadFresh(libPath, { home, googleapis })
  const mails = await lib.gmailList(2)

  assert.deepEqual(mails, [
    { id: 'm1', from: 'sender-m1@example.com', subject: 'subject-m1', date: 'date-m1' },
    { id: 'm2', from: 'sender-m2@example.com', subject: 'subject-m2', date: 'date-m2' },
  ])
  assert.equal(googleapis.calls.gmail[0].auth.clientId, 'web-client-id')
  assert.equal(googleapis.calls.gmail[0].auth.clientSecret, 'web-client-secret')
  assert.deepEqual(googleapis.calls.gmail[0].auth.credentials, { refresh_token: 'refresh-token' })
})

test('gmailListUnread uses unread query', async () => {
  const gmailClient = {
    users: {
      messages: {
        async list(args) {
          assert.deepEqual(args, { userId: 'me', maxResults: 3, q: 'is:unread' })
          return { data: { messages: [] } }
        },
        async get() {
          throw new Error('unexpected metadata fetch')
        },
      },
    },
  }
  const googleapis = createGoogleapisMock({ gmail: gmailClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  assert.deepEqual(await lib.gmailListUnread(3), [])
})

test('gmailRead decodes the plain text body', async () => {
  const gmailClient = {
    users: {
      messages: {
        async get(args) {
          assert.deepEqual(args, { userId: 'me', id: 'message-1', format: 'full' })
          return {
            data: {
              payload: {
                headers: [
                  { name: 'From', value: 'from@example.com' },
                  { name: 'To', value: 'to@example.com' },
                  { name: 'Subject', value: 'Subject line' },
                  { name: 'Date', value: 'Mon, 01 Jan 2026 00:00:00 +0000' },
                ],
                parts: [
                  {
                    mimeType: 'text/plain',
                    body: { data: Buffer.from('hello world').toString('base64') },
                  },
                ],
              },
            },
          }
        },
      },
    },
  }
  const googleapis = createGoogleapisMock({ gmail: gmailClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  assert.deepEqual(await lib.gmailRead('message-1'), {
    id: 'message-1',
    from: 'from@example.com',
    to: 'to@example.com',
    subject: 'Subject line',
    date: 'Mon, 01 Jan 2026 00:00:00 +0000',
    body: 'hello world',
  })
})

test('gmailSend and gmailMarkRead call the Gmail API with expected payloads', async () => {
  let sendArgs
  let markReadArgs
  const gmailClient = {
    users: {
      messages: {
        async send(args) {
          sendArgs = args
          return { data: { id: 'sent-1' } }
        },
        async batchModify(args) {
          markReadArgs = args
        },
      },
    },
  }
  const googleapis = createGoogleapisMock({ gmail: gmailClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  const sendResult = await lib.gmailSend('user@example.com', 'Hi', 'body text')
  await lib.gmailMarkRead(['m1', 'm2'])

  assert.deepEqual(sendResult, { id: 'sent-1' })
  assert.deepEqual(sendArgs, {
    userId: 'me',
    requestBody: {
      raw: Buffer.from(
        'To: user@example.com\r\nSubject: Hi\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody text',
      ).toString('base64url'),
    },
  })
  assert.deepEqual(markReadArgs, {
    userId: 'me',
    requestBody: { ids: ['m1', 'm2'], removeLabelIds: ['UNREAD'] },
  })
})

test('sheets list/read/write/append/create cover the Sheets API wrappers', async () => {
  let readArgs
  let writeArgs
  let appendArgs
  let createArgs
  const driveClient = {
    files: {
      async list(args) {
        assert.equal(args.q, "mimeType='application/vnd.google-apps.spreadsheet'")
        return { data: { files: [{ id: 'sheet-1', name: 'Budget', modifiedTime: '2026-04-05' }] } }
      },
    },
  }
  const sheetsClient = {
    spreadsheets: {
      values: {
        async get(args) {
          readArgs = args
          return { data: { values: [['A', 'B']] } }
        },
        async update(args) {
          writeArgs = args
        },
        async append(args) {
          appendArgs = args
        },
      },
      async create(args) {
        createArgs = args
        return { data: { spreadsheetId: 'new-sheet' } }
      },
    },
  }
  const googleapis = createGoogleapisMock({ drive: driveClient, sheets: sheetsClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  assert.deepEqual(await lib.sheetsList(), [{ id: 'sheet-1', name: 'Budget', modifiedTime: '2026-04-05' }])
  assert.deepEqual(await lib.sheetsRead('sheet-1', 'Sheet1!A1:B2'), [['A', 'B']])
  await lib.sheetsWrite('sheet-1', 'Sheet1!A1', [['Name']])
  await lib.sheetsAppend('sheet-1', 'Sheet1!A1', [['Alice']])
  assert.deepEqual(await lib.sheetsCreate('New Sheet'), { spreadsheetId: 'new-sheet' })

  assert.deepEqual(readArgs, { spreadsheetId: 'sheet-1', range: 'Sheet1!A1:B2' })
  assert.deepEqual(writeArgs, {
    spreadsheetId: 'sheet-1',
    range: 'Sheet1!A1',
    valueInputOption: 'RAW',
    requestBody: { values: [['Name']] },
  })
  assert.deepEqual(appendArgs, {
    spreadsheetId: 'sheet-1',
    range: 'Sheet1!A1',
    valueInputOption: 'RAW',
    requestBody: { values: [['Alice']] },
  })
  assert.deepEqual(createArgs, {
    requestBody: { properties: { title: 'New Sheet' } },
  })
})

test('drive list/upload/download/quota cover the Drive API wrappers', async () => {
  let listArgs
  let uploadArgs
  let downloadArgs
  let quotaArgs
  const driveClient = {
    files: {
      async list(args) {
        listArgs = args
        return { data: { files: [{ id: 'f1', name: 'doc.txt', mimeType: 'text/plain', size: '5' }] } }
      },
      async create(args) {
        uploadArgs = args
        return { data: { id: 'upload-1' } }
      },
      async get(args, options) {
        downloadArgs = { args, options }
        return { data: 'downloaded text' }
      },
    },
    about: {
      async get(args) {
        quotaArgs = args
        return { data: { storageQuota: { usage: '10', limit: '100' } } }
      },
    },
  }
  const googleapis = createGoogleapisMock({ drive: driveClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  assert.deepEqual(await lib.driveList("name contains 'doc'", 5), [
    { id: 'f1', name: 'doc.txt', mimeType: 'text/plain', size: '5' },
  ])
  assert.deepEqual(await lib.driveUpload('doc.txt', 'hello'), { id: 'upload-1' })
  assert.equal(await lib.driveDownload('file-1'), 'downloaded text')
  assert.deepEqual(await lib.driveQuota(), { usage: '10', limit: '100' })

  assert.deepEqual(listArgs, {
    q: "name contains 'doc'",
    pageSize: 5,
    fields: 'files(id, name, mimeType, modifiedTime, size)',
  })
  assert.equal(uploadArgs.requestBody.name, 'doc.txt')
  assert.equal(uploadArgs.media.mimeType, 'text/plain')
  assert.equal(uploadArgs.media.body, 'hello')
  assert.deepEqual(downloadArgs, {
    args: { fileId: 'file-1', alt: 'media' },
    options: { responseType: 'text' },
  })
  assert.deepEqual(quotaArgs, { fields: 'storageQuota' })
})

test('calendar list/events/create cover the Calendar API wrappers', async () => {
  let eventsArgs
  let createArgs
  const calendarClient = {
    calendarList: {
      async list() {
        return { data: { items: [{ id: 'primary', summary: 'Primary' }] } }
      },
    },
    events: {
      async list(args) {
        eventsArgs = args
        return { data: { items: [{ start: { dateTime: '2026-04-05T10:00:00Z' }, summary: 'Meeting' }] } }
      },
      async insert(args) {
        createArgs = args
        return { data: { id: 'event-1' } }
      },
    },
  }
  const googleapis = createGoogleapisMock({ calendar: calendarClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  assert.deepEqual(await lib.calendarList(), [{ id: 'primary', summary: 'Primary' }])
  assert.deepEqual(await lib.calendarEvents('team', 3), [
    { start: { dateTime: '2026-04-05T10:00:00Z' }, summary: 'Meeting' },
  ])
  assert.deepEqual(await lib.calendarCreate('team', 'Sync', '2026-04-05T10:00:00Z', '2026-04-05T11:00:00Z'), {
    id: 'event-1',
  })

  assert.equal(eventsArgs.calendarId, 'team')
  assert.equal(eventsArgs.maxResults, 3)
  assert.equal(eventsArgs.singleEvents, true)
  assert.equal(eventsArgs.orderBy, 'startTime')
  assert.match(eventsArgs.timeMin, /^\d{4}-\d{2}-\d{2}T/)
  assert.deepEqual(createArgs, {
    calendarId: 'team',
    requestBody: {
      summary: 'Sync',
      start: { dateTime: '2026-04-05T10:00:00Z' },
      end: { dateTime: '2026-04-05T11:00:00Z' },
    },
  })
})

test('driveUploadDir uploads folders recursively and skips excluded files', async () => {
  const sourceRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'drive-upload-'))
  fs.writeFileSync(path.join(sourceRoot, 'keep.txt'), 'keep')
  fs.writeFileSync(path.join(sourceRoot, 'skip.log'), 'skip')
  fs.mkdirSync(path.join(sourceRoot, 'nested'))
  fs.writeFileSync(path.join(sourceRoot, 'nested', 'child.txt'), 'child')

  const created = []
  let nextId = 1
  const driveClient = {
    files: {
      async create(args) {
        created.push(args)
        return { data: { id: `id-${nextId++}` } }
      },
    },
  }
  const googleapis = createGoogleapisMock({ drive: driveClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  const rootId = await lib.driveUploadDir(sourceRoot, null, ['.*\\.log$'])

  assert.equal(rootId, 'id-1')
  assert.deepEqual(
    created.map(entry => entry.requestBody.name),
    [path.basename(sourceRoot), 'keep.txt', 'nested', 'child.txt'],
  )
  assert.equal(created[0].requestBody.mimeType, 'application/vnd.google-apps.folder')
  assert.deepEqual(created[1].requestBody.parents, ['id-1'])
  assert.equal(created[1].media.body.path.endsWith('keep.txt'), true)
  assert.deepEqual(created[3].requestBody.parents, ['id-3'])
})

test('driveDownloadDir downloads nested folders recursively', async () => {
  const outputRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'drive-download-'))
  const metadata = {
    root: { name: 'root', mimeType: 'application/vnd.google-apps.folder' },
    file1: { name: 'file1.txt', mimeType: 'text/plain' },
    sub: { name: 'nested', mimeType: 'application/vnd.google-apps.folder' },
    file2: { name: 'file2.txt', mimeType: 'text/plain' },
  }
  const children = {
    root: [{ id: 'file1', name: 'file1.txt', mimeType: 'text/plain' }, { id: 'sub', name: 'nested', mimeType: 'application/vnd.google-apps.folder' }],
    sub: [{ id: 'file2', name: 'file2.txt', mimeType: 'text/plain' }],
  }

  const driveClient = {
    files: {
      async get(args, options) {
        if (args.alt === 'media') {
          return { data: streamWith(args.fileId === 'file1' ? 'root-file' : 'nested-file') }
        }
        return { data: metadata[args.fileId] }
      },
      async list(args) {
        const match = args.q.match(/^'(.+)' in parents$/)
        return { data: { files: children[match[1]] || [] } }
      },
    },
  }
  const googleapis = createGoogleapisMock({ drive: driveClient })
  const lib = loadFresh(libPath, { home: makeTempHome(), googleapis })

  await lib.driveDownloadDir('root', outputRoot)

  assert.equal(fs.readFileSync(path.join(outputRoot, 'root', 'file1.txt'), 'utf8'), 'root-file')
  assert.equal(fs.readFileSync(path.join(outputRoot, 'root', 'nested', 'file2.txt'), 'utf8'), 'nested-file')
})
