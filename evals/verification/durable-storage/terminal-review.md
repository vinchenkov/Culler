# Terminal review

The reviewer accepted the terminal text.

The review used actual command output at 80 and 40 columns.
Each width used color and plain terminal environments.
The command emits no ANSI color sequences in either environment.
The browser server opened an idle loopback listener and then closed it.
The review made no model calls.

## Review criteria

- The help text states the command purpose and each option.
- The help text preserves the complete storage path at both widths.
- The temporary-path error states the required action.
- The recovery help states its purpose and preserves existing records.
- The recovery help states that recovery makes no model calls.
- The browser startup states the experiment location and start controls.
- The startup text distinguishes server activation from model calls.
- The final status correctly reports that the browser closed.

The text remains concise and gives specific actions.
The review found no false completion claim or concealed model invocation.
The capture script reproduces the four output records.

## Existing backup refusal

The reviewer accepted the additional refusal message.
The review captured actual output at both widths and in both color modes.
The test created a backup and removed only its isolated primary directory.
The next backup request refused the same experiment path.
The test confirmed that the refusal preserved each backup file.
The message explains the refusal and gives a specific action.
The message makes no false completion claim.
The review made no model calls.
The isolated test directories were removed after each capture.
