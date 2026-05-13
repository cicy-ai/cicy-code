#!/usr/bin/env node

const assert = require('node:assert/strict')
const fs = require('node:fs')
const fsp = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { spawnSync } = require('node:child_process')
const { google } = require('googleapis')

const providerDir = path.resolve(__dirname, '..')
const globalPath = path.join(process.env.HOME, 'global.json')
const globalConfig = JSON.parse(fs.readFileSync(globalPath, 'utf8'))

const oauth2 = new google.auth.OAuth2(
  globalConfig.GMAIL_WEB_CLIENT_ID || globalConfig.GMAIL_CLIENT_ID,
  globalConfig.GMAIL_WEB_CLIENT_SECRET || globalConfig.GMAIL_CLIENT_SECRET,
)
oauth2.setCredentials({ refresh_token: globalConfig.GMAIL_REFRESH_TOKEN })

const gmail = google.gmail({ version: 'v1', auth: oauth2 })
const drive = google.drive({ version: 'v3', auth: oauth2 })
const calendar = google.calendar({ version: 'v3', auth: oauth2 })
const sheets = google.sheets({ version: 'v4', auth: oauth2 })

function log(step, message) {
  process.stdout.write(`[live:${step}] ${message}\n`)
}

function runNode(script, args, { input, timeout = 180000 } = {}) {
  const result = spawnSync('node', [script, ...args], {
    cwd: providerDir,
    env: process.env,
    encoding: 'utf8',
    input,
    timeout,
  })

  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    throw new Error(
      `command failed: node ${script} ${args.join(' ')}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`,
    )
  }

  return (result.stdout || '').trim()
}

async function waitFor(description, fn, { timeoutMs = 45000, intervalMs = 2000 } = {}) {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    const value = await fn()
    if (value) {
      return value
    }
    await new Promise(resolve => setTimeout(resolve, intervalMs))
  }
  throw new Error(`timed out waiting for ${description}`)
}

async function gmailSearch(query) {
  const res = await gmail.users.messages.list({ userId: 'me', q: query, maxResults: 20 })
  return res.data.messages || []
}

async function gmailMessage(id, format = 'full') {
  const res = await gmail.users.messages.get({ userId: 'me', id, format })
  return res.data
}

async function gmailModify(ids, requestBody) {
  if (!ids.length) {
    return
  }
  await gmail.users.messages.batchModify({ userId: 'me', requestBody: { ids, ...requestBody } })
}

async function driveDelete(fileId) {
  try {
    await drive.files.delete({ fileId })
  } catch (error) {
    if (error?.code !== 404) {
      throw error
    }
  }
}

async function calendarDelete(eventId) {
  try {
    await calendar.events.delete({ calendarId: 'primary', eventId })
  } catch (error) {
    if (error?.code !== 404) {
      throw error
    }
  }
}

async function main() {
  const profile = await gmail.users.getProfile({ userId: 'me' })
  const selfEmail = profile.data.emailAddress
  const tag = `cicy-live-${Date.now()}`
  const code = '654321'
  const gmailSubject = `CiCy Live Gmail ${tag}`
  const gmailBody = `Live integration code ${code}`
  const sheetTitle = `CiCy Live Sheet ${tag}`
  const driveFileName = `${tag}.txt`
  const driveFileContent = `drive-content-${tag}`
  const calendarSummary = `CiCy Live Event ${tag}`

  const cleanup = {
    gmailQueries: new Set([`subject:"${gmailSubject}" newer_than:2d`]),
    driveFileIds: new Set(),
    calendarEventIds: new Set(),
    tempPaths: new Set(),
  }

  try {
    log('gmail', 'send')
    assert.equal(
      runNode('google.js', ['gmail', 'send', selfEmail, gmailSubject, gmailBody]),
      'Sent.',
    )

    const gmailMessages = await waitFor('sent gmail message', async () => {
      const messages = await gmailSearch(`subject:"${gmailSubject}" newer_than:2d`)
      return messages.length ? messages : null
    })
    const gmailMessageIds = gmailMessages.map(item => item.id)
    const gmailMessageId = gmailMessageIds[0]

    log('gmail', 'list')
    const gmailListOutput = runNode('google.js', ['gmail', 'list', '10'])
    assert.match(gmailListOutput, new RegExp(tag))

    log('gmail', 'read')
    const gmailReadOutput = runNode('google.js', ['gmail', 'read', gmailMessageId])
    assert.match(gmailReadOutput, new RegExp(code))

    log('gmail', 'read-all')
    const cacheDir = path.join(process.env.HOME, '.cache')
    fs.mkdirSync(cacheDir, { recursive: true })
    fs.writeFileSync(path.join(cacheDir, 'gmail-ids.json'), JSON.stringify([gmailMessageId]))
    await gmailModify([gmailMessageId], { addLabelIds: ['UNREAD'] })
    assert.equal(runNode('google.js', ['gmail', 'read-all']), 'Marked 1 emails as read.')
    const afterReadAll = await gmailMessage(gmailMessageId, 'metadata')
    assert(!afterReadAll.labelIds?.includes('UNREAD'), 'expected message to be marked read')

    log('gmail', 'watch')
    await gmailModify([gmailMessageId], { addLabelIds: ['UNREAD'] })
    const gmailWatchOutput = runNode('google.js', ['gmail', 'watch', tag], { timeout: 30000 })
    assert.match(gmailWatchOutput, new RegExp(code))

    log('sheets', 'create/list/write/append/read')
    const createSheetOutput = runNode('google.js', ['sheets', 'create', sheetTitle])
    const spreadsheetId = createSheetOutput.replace(/^Created:\s*/, '')
    assert(spreadsheetId, 'expected spreadsheet id')
    cleanup.driveFileIds.add(spreadsheetId)
    const spreadsheet = await sheets.spreadsheets.get({ spreadsheetId })
    const defaultSheetTitle = spreadsheet.data.sheets?.[0]?.properties?.title
    assert(defaultSheetTitle, 'expected default sheet title')

    const sheetsListOutput = runNode('google.js', ['sheets', 'list'])
    assert.match(sheetsListOutput, new RegExp(tag))

    assert.equal(
      runNode('google.js', ['sheets', 'write', spreadsheetId, `${defaultSheetTitle}!A1:B2`, '[["Name","Value"],["tag","live"]]']),
      'Written.',
    )
    assert.equal(
      runNode('google.js', ['sheets', 'append', spreadsheetId, `${defaultSheetTitle}!A1`, '[["append","ok"]]']),
      'Appended.',
    )
    const sheetsReadOutput = runNode('google.js', ['sheets', 'read', spreadsheetId, `${defaultSheetTitle}!A1:B3`])
    assert.match(sheetsReadOutput, /Name\tValue/)
    assert.match(sheetsReadOutput, /tag\tlive/)
    assert.match(sheetsReadOutput, /append\tok/)

    log('drive', 'upload/list/download/quota')
    const driveUploadOutput = runNode('google.js', ['drive', 'upload', driveFileName, driveFileContent])
    const uploadedFileId = driveUploadOutput.replace(/^Uploaded:\s*/, '')
    assert(uploadedFileId, 'expected uploaded drive file id')
    cleanup.driveFileIds.add(uploadedFileId)

    const driveListOutput = runNode('google.js', ['drive', 'list', `name = '${driveFileName}'`, '10'])
    assert.match(driveListOutput, new RegExp(driveFileName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))

    const driveDownloadOutput = runNode('google.js', ['drive', 'download', uploadedFileId])
    assert.equal(driveDownloadOutput, driveFileContent)

    const driveQuotaOutput = runNode('google.js', ['drive', 'quota'])
    assert.match(driveQuotaOutput, /^Used: /)

    log('drive', 'upload-dir/download-dir')
    const uploadRoot = await fsp.mkdtemp(path.join(os.tmpdir(), `${tag}-upload-`))
    cleanup.tempPaths.add(uploadRoot)
    await fsp.writeFile(path.join(uploadRoot, 'keep.txt'), `keep-${tag}`)
    await fsp.writeFile(path.join(uploadRoot, 'skip.tmp'), `skip-${tag}`)
    await fsp.mkdir(path.join(uploadRoot, 'nested'))
    await fsp.writeFile(path.join(uploadRoot, 'nested', 'child.txt'), `child-${tag}`)

    const uploadDirOutput = runNode('google.js', ['drive', 'upload-dir', uploadRoot, '--exclude', '^skip\\.tmp$'])
    const uploadedDirId = uploadDirOutput.replace(/^Uploaded:\s*/, '')
    assert(uploadedDirId, 'expected uploaded drive directory id')
    cleanup.driveFileIds.add(uploadedDirId)

    const downloadRoot = await fsp.mkdtemp(path.join(os.tmpdir(), `${tag}-download-`))
    cleanup.tempPaths.add(downloadRoot)
    assert.equal(runNode('google.js', ['drive', 'download-dir', uploadedDirId, downloadRoot]), 'Downloaded.')
    const downloadedBase = path.join(downloadRoot, path.basename(uploadRoot))
    assert.equal(await fsp.readFile(path.join(downloadedBase, 'keep.txt'), 'utf8'), `keep-${tag}`)
    assert.equal(await fsp.readFile(path.join(downloadedBase, 'nested', 'child.txt'), 'utf8'), `child-${tag}`)
    assert.equal(fs.existsSync(path.join(downloadedBase, 'skip.tmp')), false)

    log('calendar', 'list/create/events')
    const calendarListOutput = runNode('google.js', ['calendar', 'list'])
    assert.match(calendarListOutput, new RegExp(selfEmail.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))

    const start = new Date(Date.now() + 10 * 60 * 1000)
    const end = new Date(Date.now() + 20 * 60 * 1000)
    const calendarCreateOutput = runNode('google.js', [
      'calendar',
      'create',
      calendarSummary,
      start.toISOString(),
      end.toISOString(),
    ])
    const calendarEventId = calendarCreateOutput.replace(/^Created:\s*/, '')
    assert(calendarEventId, 'expected calendar event id')
    cleanup.calendarEventIds.add(calendarEventId)

    const calendarEventsOutput = await waitFor('calendar event visibility', async () => {
      const output = runNode('google.js', ['calendar', 'events', 'primary', '20'])
      return output.includes(calendarSummary) ? output : null
    }, { timeoutMs: 30000, intervalMs: 3000 })
    assert.match(calendarEventsOutput, new RegExp(tag))

    log('done', 'all live integration checks passed')
  } finally {
    for (const eventId of cleanup.calendarEventIds) {
      await calendarDelete(eventId)
    }

    for (const fileId of cleanup.driveFileIds) {
      await driveDelete(fileId)
    }

    for (const query of cleanup.gmailQueries) {
      const messages = await gmailSearch(query)
      for (const message of messages) {
        try {
          await gmail.users.messages.trash({ userId: 'me', id: message.id })
        } catch (error) {
          if (error?.code !== 404) {
            throw error
          }
        }
      }
    }

    for (const tempPath of cleanup.tempPaths) {
      await fsp.rm(tempPath, { recursive: true, force: true })
    }
  }
}

main().catch(error => {
  console.error(error.stack || error.message || String(error))
  process.exit(1)
})
