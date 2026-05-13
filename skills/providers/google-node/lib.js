const { google } = require('googleapis')
const { resolve } = require('path')

const GLOBAL = require(resolve(process.env.HOME, 'cicy-ai', 'global.json'))

function getAuth() {
  const oauth2 = new google.auth.OAuth2(GLOBAL.GMAIL_WEB_CLIENT_ID || GLOBAL.GMAIL_CLIENT_ID, GLOBAL.GMAIL_WEB_CLIENT_SECRET || GLOBAL.GMAIL_CLIENT_SECRET)
  oauth2.setCredentials({ refresh_token: GLOBAL.GMAIL_REFRESH_TOKEN })
  return oauth2
}

// ===== Gmail =====
async function gmailList(n = 10) {
  const gmail = google.gmail({ version: 'v1', auth: getAuth() })
  const res = await gmail.users.messages.list({ userId: 'me', maxResults: n })
  const results = []
  for (const m of res.data.messages || []) {
    const msg = await gmail.users.messages.get({ userId: 'me', id: m.id, format: 'metadata', metadataHeaders: ['From', 'Subject', 'Date'] })
    const h = Object.fromEntries(msg.data.payload.headers.map(h => [h.name, h.value]))
    results.push({ id: m.id, from: h.From, subject: h.Subject, date: h.Date })
  }
  return results
}

async function gmailListUnread(n = 10) {
  const gmail = google.gmail({ version: 'v1', auth: getAuth() })
  const res = await gmail.users.messages.list({ userId: 'me', maxResults: n, q: 'is:unread' })
  const results = []
  for (const m of res.data.messages || []) {
    const msg = await gmail.users.messages.get({ userId: 'me', id: m.id, format: 'metadata', metadataHeaders: ['From', 'Subject', 'Date'] })
    const h = Object.fromEntries(msg.data.payload.headers.map(h => [h.name, h.value]))
    results.push({ id: m.id, from: h.From, subject: h.Subject, date: h.Date })
  }
  return results
}

async function gmailRead(messageId) {
  const gmail = google.gmail({ version: 'v1', auth: getAuth() })
  const msg = await gmail.users.messages.get({ userId: 'me', id: messageId, format: 'full' })
  const headers = Object.fromEntries(msg.data.payload.headers.map(h => [h.name, h.value]))
  const body = Buffer.from(
    msg.data.payload.parts?.find(p => p.mimeType === 'text/plain')?.body?.data ||
    msg.data.payload.body?.data || '', 'base64'
  ).toString()
  return { id: messageId, from: headers.From, to: headers.To, subject: headers.Subject, date: headers.Date, body }
}

async function gmailSend(to, subject, body) {
  const gmail = google.gmail({ version: 'v1', auth: getAuth() })
  const raw = Buffer.from(
    `To: ${to}\r\nSubject: ${subject}\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n${body}`
  ).toString('base64url')
  const res = await gmail.users.messages.send({ userId: 'me', requestBody: { raw } })
  return res.data
}

async function gmailMarkRead(messageIds) {
  const gmail = google.gmail({ version: 'v1', auth: getAuth() })
  await gmail.users.messages.batchModify({ userId: 'me', requestBody: { ids: messageIds, removeLabelIds: ['UNREAD'] } })
}

// ===== Sheets =====
async function sheetsList() {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const res = await drive.files.list({ q: "mimeType='application/vnd.google-apps.spreadsheet'", pageSize: 20, fields: 'files(id, name, modifiedTime)' })
  return res.data.files || []
}

async function sheetsRead(spreadsheetId, range) {
  const sheets = google.sheets({ version: 'v4', auth: getAuth() })
  const res = await sheets.spreadsheets.values.get({ spreadsheetId, range })
  return res.data.values || []
}

async function sheetsWrite(spreadsheetId, range, values) {
  const sheets = google.sheets({ version: 'v4', auth: getAuth() })
  await sheets.spreadsheets.values.update({ spreadsheetId, range, valueInputOption: 'RAW', requestBody: { values } })
}

async function sheetsAppend(spreadsheetId, range, values) {
  const sheets = google.sheets({ version: 'v4', auth: getAuth() })
  await sheets.spreadsheets.values.append({ spreadsheetId, range, valueInputOption: 'RAW', requestBody: { values } })
}

async function sheetsCreate(title) {
  const sheets = google.sheets({ version: 'v4', auth: getAuth() })
  const res = await sheets.spreadsheets.create({ requestBody: { properties: { title } } })
  return res.data
}

// ===== Drive =====
async function driveList(query = '', pageSize = 20) {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const res = await drive.files.list({ q: query, pageSize, fields: 'files(id, name, mimeType, modifiedTime, size)' })
  return res.data.files || []
}

async function driveUpload(name, content, mimeType = 'text/plain') {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const res = await drive.files.create({
    requestBody: { name },
    media: { mimeType, body: content }
  })
  return res.data
}

async function driveDownload(fileId) {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const res = await drive.files.get({ fileId, alt: 'media' }, { responseType: 'text' })
  return res.data
}

// ===== Calendar =====
async function calendarList() {
  const calendar = google.calendar({ version: 'v3', auth: getAuth() })
  const res = await calendar.calendarList.list()
  return res.data.items || []
}

async function calendarEvents(calendarId = 'primary', maxResults = 10) {
  const calendar = google.calendar({ version: 'v3', auth: getAuth() })
  const res = await calendar.events.list({ calendarId, maxResults, singleEvents: true, orderBy: 'startTime', timeMin: new Date().toISOString() })
  return res.data.items || []
}

async function calendarCreate(calendarId = 'primary', summary, start, end) {
  const calendar = google.calendar({ version: 'v3', auth: getAuth() })
  const res = await calendar.events.insert({
    calendarId,
    requestBody: {
      summary,
      start: { dateTime: start },
      end: { dateTime: end }
    }
  })
  return res.data
}

async function driveUploadDir(localPath, parentId = null, exclude = []) {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const fs = require('fs')
  const path = require('path')
  
  const stats = fs.statSync(localPath)
  const name = path.basename(localPath)
  
  // Check exclude patterns
  if (exclude.some(pattern => name.match(new RegExp(pattern)))) {
    return null
  }
  
  if (stats.isDirectory()) {
    // Create folder
    const folderMeta = { name, mimeType: 'application/vnd.google-apps.folder' }
    if (parentId) folderMeta.parents = [parentId]
    const folder = await drive.files.create({ requestBody: folderMeta, fields: 'id' })
    const folderId = folder.data.id
    
    // Upload children
    const children = fs.readdirSync(localPath)
    for (const child of children) {
      await driveUploadDir(path.join(localPath, child), folderId, exclude)
    }
    return folderId
  } else {
    // Upload file
    const fileMeta = { name }
    if (parentId) fileMeta.parents = [parentId]
    const media = { body: fs.createReadStream(localPath) }
    const file = await drive.files.create({ requestBody: fileMeta, media, fields: 'id' })
    return file.data.id
  }
}

async function driveDownloadDir(fileId, localPath) {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const fs = require('fs')
  const path = require('path')
  
  // Get file metadata
  const file = await drive.files.get({ fileId, fields: 'name,mimeType' })
  const { name, mimeType } = file.data
  const targetPath = path.join(localPath, name)
  
  if (mimeType === 'application/vnd.google-apps.folder') {
    // Create local directory
    fs.mkdirSync(targetPath, { recursive: true })
    
    // List children
    const res = await drive.files.list({ q: `'${fileId}' in parents`, fields: 'files(id,name,mimeType)' })
    for (const child of res.data.files || []) {
      await driveDownloadDir(child.id, targetPath)
    }
  } else {
    // Download file
    const dest = fs.createWriteStream(targetPath)
    const res = await drive.files.get({ fileId, alt: 'media' }, { responseType: 'stream' })
    await new Promise((resolve, reject) => {
      res.data.pipe(dest)
        .on('finish', resolve)
        .on('error', reject)
    })
  }
}

async function driveQuota() {
  const drive = google.drive({ version: 'v3', auth: getAuth() })
  const res = await drive.about.get({ fields: 'storageQuota' })
  return res.data.storageQuota
}

module.exports = {
  gmailList, gmailListUnread, gmailRead, gmailSend, gmailMarkRead,
  sheetsList, sheetsRead, sheetsWrite, sheetsAppend, sheetsCreate,
  driveList, driveUpload, driveDownload, driveUploadDir, driveDownloadDir, driveQuota,
  calendarList, calendarEvents, calendarCreate
}
