#!/usr/bin/env node

// Google Skills CLI
// Usage: google <service> <command> [args]

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

  const [service, cmd, ...args] = argv
  const cacheDeps = { env, fsImpl }

  if (!service || isHelpArg(service)) {
    printGoogleUsage(print)
    if (service === 'help' && cmd) {
      print('')
      printServiceHelp(cmd, print)
    }
    return
  }

  if (service === 'gmail') {
    switch (cmd) {
      case 'help':
      case '-h':
      case '--help':
      case undefined:
        printGoogleGmailUsage(print)
        break
      case 'list': {
        const mails = await lib.gmailList(Number(args[0]) || 10)
        saveIds(mails, cacheDeps)
        mails.forEach((m, i) => print(`${i + 1}  ${m.date}  ${m.from}  ${m.subject}`))
        break
      }
      case 'read': {
        if (!args[0]) { error('Usage: google gmail read <n>'); exit(1); return }
        const m = await lib.gmailRead(resolveId(args[0], cacheDeps))
        print(`From: ${m.from}\nTo: ${m.to}\nDate: ${m.date}\nSubject: ${m.subject}\n\n${m.body}`)
        break
      }
      case 'read-all': {
        const ids = loadIds(cacheDeps)
        if (!ids.length) { error('Run "google gmail list" first'); exit(1); return }
        await lib.gmailMarkRead(ids)
        print(`Marked ${ids.length} emails as read.`)
        break
      }
      case 'send': {
        if (args.length < 2) { error('Usage: google gmail send <to> <subject> [body]'); exit(1); return }
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
        printGoogleGmailUsage(print)
    }
  } else if (service === 'sheets') {
    switch (cmd) {
      case 'help':
      case '-h':
      case '--help':
      case undefined:
        printGoogleSheetsUsage(print)
        break
      case 'list': {
        const files = await lib.sheetsList()
        files.forEach(f => print(`${f.id}  ${f.name}  ${f.modifiedTime}`))
        break
      }
      case 'read': {
        if (args.length < 2) { error('Usage: google sheets read <id> <range>'); exit(1); return }
        const rows = await lib.sheetsRead(args[0], args[1])
        rows.forEach(r => print(r.join('\t')))
        break
      }
      case 'write': {
        if (args.length < 3) { error('Usage: google sheets write <id> <range> <values_json>'); exit(1); return }
        await lib.sheetsWrite(args[0], args[1], JSON.parse(args[2]))
        print('Written.')
        break
      }
      case 'append': {
        if (args.length < 3) { error('Usage: google sheets append <id> <range> <values_json>'); exit(1); return }
        await lib.sheetsAppend(args[0], args[1], JSON.parse(args[2]))
        print('Appended.')
        break
      }
      case 'create': {
        if (!args[0]) { error('Usage: google sheets create <title>'); exit(1); return }
        const sheet = await lib.sheetsCreate(args[0])
        print(`Created: ${sheet.spreadsheetId}`)
        break
      }
      default:
        printGoogleSheetsUsage(print)
    }
  } else if (service === 'drive') {
    switch (cmd) {
      case 'help':
      case '-h':
      case '--help':
      case undefined:
        printGoogleDriveUsage(print)
        break
      case 'list': {
        const files = await lib.driveList(args[0] || '', Number(args[1]) || 20)
        files.forEach(f => print(`${f.id}  ${f.name}  ${f.mimeType}  ${f.size || '-'}`))
        break
      }
      case 'upload': {
        if (args.length < 2) { error('Usage: google drive upload <name> <content>'); exit(1); return }
        const file = await lib.driveUpload(args[0], args[1])
        print(`Uploaded: ${file.id}`)
        break
      }
      case 'download': {
        if (!args[0]) { error('Usage: google drive download <id>'); exit(1); return }
        const content = await lib.driveDownload(args[0])
        print(content)
        break
      }
      case 'upload-dir': {
        if (!args[0]) { error('Usage: google drive upload-dir <path> [--exclude pattern1,pattern2]'); exit(1); return }
        const excludeIdx = args.indexOf('--exclude')
        const exclude = excludeIdx !== -1 ? args[excludeIdx + 1].split(',') : []
        const rootId = await lib.driveUploadDir(args[0], null, exclude)
        print(`Uploaded: ${rootId}`)
        break
      }
      case 'download-dir': {
        if (args.length < 2) { error('Usage: google drive download-dir <id> <local_path>'); exit(1); return }
        await lib.driveDownloadDir(args[0], args[1])
        print('Downloaded.')
        break
      }
      case 'quota': {
        const quota = await lib.driveQuota()
        const used = Number(quota.usage) / (1024**3)
        const limit = Number(quota.limit) / (1024**3)
        const percent = (used / limit * 100).toFixed(2)
        print(`Used: ${used.toFixed(2)} GB / ${limit.toFixed(2)} GB (${percent}%)`)
        if (quota.usageInDrive) print(`  Drive: ${(Number(quota.usageInDrive) / (1024**3)).toFixed(2)} GB`)
        if (quota.usageInDriveTrash) print(`  Trash: ${(Number(quota.usageInDriveTrash) / (1024**3)).toFixed(2)} GB`)
        break
      }
      default:
        printGoogleDriveUsage(print)
    }
  } else if (service === 'calendar') {
    switch (cmd) {
      case 'help':
      case '-h':
      case '--help':
      case undefined:
        printGoogleCalendarUsage(print)
        break
      case 'list': {
        const cals = await lib.calendarList()
        cals.forEach(c => print(`${c.id}  ${c.summary}`))
        break
      }
      case 'events': {
        const events = await lib.calendarEvents(args[0] || 'primary', Number(args[1]) || 10)
        events.forEach(e => print(`${e.start.dateTime || e.start.date}  ${e.summary}`))
        break
      }
      case 'create': {
        if (args.length < 3) { error('Usage: google calendar create <summary> <start_iso> <end_iso>'); exit(1); return }
        const event = await lib.calendarCreate('primary', args[0], args[1], args[2])
        print(`Created: ${event.id}`)
        break
      }
      default:
        printGoogleCalendarUsage(print)
    }
  } else {
    printGoogleUsage(print)
  }
}

function isHelpArg(value) {
  return value === 'help' || value === '-h' || value === '--help'
}

function printGoogleUsage(print) {
  print('Available services:')
  print('  gmail      - Email management')
  print('  sheets     - Spreadsheet operations')
  print('  drive      - File storage')
  print('  calendar   - Calendar events')
  print('\nUsage: google <service> <command> [args]')
  print('       google <service> help')
  print('       google help [service]')
}

function printServiceHelp(service, print) {
  switch (service) {
    case 'gmail':
      return printGoogleGmailUsage(print)
    case 'sheets':
      return printGoogleSheetsUsage(print)
    case 'drive':
      return printGoogleDriveUsage(print)
    case 'calendar':
      return printGoogleCalendarUsage(print)
    default:
      printGoogleUsage(print)
  }
}

function printGoogleGmailUsage(print) {
  print('Usage: google gmail <list|read|read-all|send|watch>')
  print('\nCommands:')
  print('  list [count]              List recent emails (default 10)')
  print('  read <n>                  Read email by index from last list')
  print('  read-all                  Mark all listed emails as read')
  print('  send <to> <subject> [body]  Send email')
  print('  watch [keyword]           Watch for new emails with verification codes')
}

function printGoogleSheetsUsage(print) {
  print('Usage: google sheets <list|read|write|append|create>')
  print('\nCommands:')
  print('  list                      List all spreadsheets')
  print('  read <id> <range>         Read cells (e.g. Sheet1!A1:B10)')
  print('  write <id> <range> <json> Write cells (JSON array)')
  print('  append <id> <range> <json> Append rows (JSON array)')
  print('  create <title>            Create new spreadsheet')
}

function printGoogleDriveUsage(print) {
  print('Usage: google drive <list|upload|upload-dir|download|download-dir|quota>')
  print('\nCommands:')
  print('  list [query] [pageSize]   List files (optional query filter)')
  print('  upload <name> <content>   Upload text file')
  print('  upload-dir <path> [--exclude patterns]  Upload directory')
  print('  download <id>             Download file content')
  print('  download-dir <id> <path>  Download folder recursively')
  print('  quota                     Show storage usage')
}

function printGoogleCalendarUsage(print) {
  print('Usage: google calendar <list|events|create>')
  print('\nCommands:')
  print('  list                      List all calendars')
  print('  events [calId] [max]      List upcoming events (default: primary, 10)')
  print('  create <summary> <start> <end>  Create event (ISO 8601 timestamps)')
}

if (require.main === module) {
  main().catch(e => { console.error(e.message); process.exit(1) })
}

module.exports = {
  main,
}
