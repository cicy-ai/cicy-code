// One-time OAuth2 authorization script
// Usage: node scripts/get-token.js
const { google } = require('googleapis')
const http = require('http')
const { resolve } = require('path')

const GLOBAL = require(resolve(process.env.HOME, 'cicy-ai', 'global.json'))
const PORT = 3333

const oauth2 = new google.auth.OAuth2(
  GLOBAL.GMAIL_CLIENT_ID,
  GLOBAL.GMAIL_CLIENT_SECRET,
  `http://localhost:${PORT}`
)

const url = oauth2.generateAuthUrl({
  access_type: 'offline',
  scope: [
    'https://mail.google.com/',
    'https://www.googleapis.com/auth/spreadsheets',
    'https://www.googleapis.com/auth/drive',
    'https://www.googleapis.com/auth/calendar',
    'https://www.googleapis.com/auth/cloud-platform'
  ],
  prompt: 'consent'
})

console.log('\nOpen this URL to authorize:\n' + url + '\n')

http.createServer(async (req, res) => {
  const code = new URL(req.url, `http://localhost:${PORT}`).searchParams.get('code')
  if (!code) return res.end('no code')
  const { tokens } = await oauth2.getToken(code)
  console.log('\nrefresh_token:', tokens.refresh_token)
  console.log('\nAdd to ~/cicy-ai/global.json as GMAIL_REFRESH_TOKEN')
  console.log('This token will also work for Google Cloud STT/TTS via cloud-platform scope.')
  res.end('Done! Check terminal.')
  process.exit()
}).listen(PORT, () => console.log(`Waiting for callback on :${PORT}...`))
