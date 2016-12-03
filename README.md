# Helper for JOI

This is a testing and uploading helper for Japanese Olympiad in Informatics.

## Usage

```
export JSESSIONID=[cookie]
export JOITR=[pr/yo]
joihelper -e a.out -q 1 -s main.cpp
```

This application uses inotify. It reruns test when the executable is modified. After all the test case are passed, your source code is uploaded with all the processed outputs.
