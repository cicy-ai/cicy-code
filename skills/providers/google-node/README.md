# google-node provider

This Node.js provider is embedded inside `cicy-skills`.

Provider path:

- `~/projects/cicy-skills/providers/google-node`

Google API CLI tools with OAuth2: Gmail, Sheets, Drive, Calendar.

## Setup

1. Create OAuth2 credentials in [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
2. Enable APIs: Gmail, Sheets, Drive, Calendar, People
3. Run `node scripts/get-token.js` to authorize
4. Add to `~/cicy-ai/global.json`:
   ```json
   {
     "GMAIL_CLIENT_ID": "...",
     "GMAIL_CLIENT_SECRET": "...",
     "GMAIL_REFRESH_TOKEN": "..."
   }
   ```

## Install

```bash
cicy-skills install google-node
```

## Usage

### Gmail

```bash
google gmail list 5
google gmail send user@x.com "Hi" "body"
google gmail read 1
google gmail watch "verification"
```

### Sheets

```bash
google sheets list                         # List spreadsheets
google sheets read <id> <range>            # Read cells (e.g. Sheet1!A1:B10)
google sheets write <id> <range> <json>    # Write cells
google sheets append <id> <range> <json>   # Append rows
google sheets create <title>               # Create new sheet
```

**Examples:**
```bash
google sheets list
google sheets read 11HQWfcD... "Sheet1!A1:C10"
google sheets write 11HQWfcD... "Sheet1!A1" '[["Name","Age"],["Alice","30"]]'
google sheets append 11HQWfcD... "Sheet1!A1" '[["Bob","25"]]'
google sheets create "My Sheet"
```

### Drive

```bash
google drive list [query] [pageSize]       # List files
google drive upload <name> <content>       # Upload text file
google drive upload-dir <path> [--exclude patterns]  # Upload directory
google drive download <id>                 # Download file content
google drive download-dir <id> <path>      # Download folder recursively
google drive quota                         # Show storage usage
```

**Examples:**
```bash
google drive list
google drive list "name contains 'test'" 10
google drive upload "test.txt" "hello world"
google drive upload-dir /path/to/dir --exclude "node_modules,*.log,dist"
google drive download 15K71Biw0...
google drive download-dir <folder_id> /local/path
google drive quota
```

### Calendar

```bash
google calendar list                       # List calendars
google calendar events [calId] [max]       # List upcoming events
google calendar create <summary> <start_iso> <end_iso>  # Create event
```

**Examples:**
```bash
google calendar list
google calendar events primary 5
google calendar create "Meeting" "2026-03-10T10:00:00Z" "2026-03-10T11:00:00Z"
```

## Library Usage

```javascript
const { gmailList, sheetsRead, driveList, calendarEvents } = require('./lib')

const mails = await gmailList(10)
const rows = await sheetsRead('spreadsheet_id', 'Sheet1!A1:B10')
const files = await driveList('', 20)
const events = await calendarEvents('primary', 10)
```
