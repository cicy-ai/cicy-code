# google

Google API CLI tools: Gmail, Sheets, Drive, Calendar.

## Commands

### Gmail
```bash
google gmail list [count]                  # List recent emails
google gmail read <n>                      # Read email by index
google gmail read-all                      # Mark all as read
google gmail send <to> <subject> [body]    # Send email
google gmail watch [keyword]               # Watch for verification codes
```

### Sheets
```bash
google sheets list                         # List spreadsheets
google sheets read <id> <range>            # Read cells
google sheets write <id> <range> <json>    # Write cells
google sheets append <id> <range> <json>   # Append rows
google sheets create <title>               # Create new sheet
```

### Drive
```bash
google drive list [query] [pageSize]       # List files
google drive upload <name> <content>       # Upload text file
google drive upload-dir <path> [--exclude patterns]  # Upload directory
google drive download <id>                 # Download file
google drive download-dir <id> <path>      # Download folder recursively
google drive quota                         # Show storage usage
```

### Calendar
```bash
google calendar list                       # List calendars
google calendar events [calId] [max]       # List upcoming events
google calendar create <summary> <start> <end>  # Create event
```

## Examples

```bash
# Gmail
google gmail list 5
google gmail watch "verification"
google gmail send user@x.com "Hi" "body"

# Sheets
google sheets list
google sheets read 11HQWfcD... "Sheet1!A1:C10"
google sheets write 11HQWfcD... "Sheet1!A1" '[["Name","Age"]]'

# Drive
google drive list
google drive upload "test.txt" "content"
google drive upload-dir /path/to/dir --exclude "node_modules,*.log"
google drive download-dir <folder_id> /local/path
google drive quota

# Calendar
google calendar events primary 5
google calendar create "Meeting" "2026-03-10T10:00:00Z" "2026-03-10T11:00:00Z"
```

**Project:** `~/projects/cicy-skills/providers/google-node`
**GitHub:** https://github.com/cicy-ai/cicy-skills
**Location:** `~/projects/cicy-skills/bin/google`, `~/.local/bin/google`
