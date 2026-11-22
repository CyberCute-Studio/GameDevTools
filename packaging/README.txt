CyberCute Dev Tools
===================

A Windows 11 (64-bit) desktop toolbox for exact-size screenshots and batch
image resizing. It works offline and needs no administrator rights.

Run it
------
Extract the whole ZIP to a folder you can write to, such as
Documents\CyberCute Dev Tools, then open cybercute-dev-tools.exe. Nothing is
installed. Pin it to Start or the taskbar if you want a shortcut.

This build is not code-signed, so Windows SmartScreen may say it does not
recognize the app. Check the download first (below), then choose
More info > Run anyway.

Check the download
------------------
Each release publishes a .sha256 file beside the ZIP. In PowerShell:

    Get-FileHash .\cybercute-dev-tools-<version>-windows-x64.zip -Algorithm SHA256

The hash must match the first value in the .sha256 file.

Your files
----------
Settings:     %APPDATA%\CyberCute Dev Tools
Logs:         %LOCALAPPDATA%\CyberCute Dev Tools\Logs
Screenshots:  Pictures\CyberCute Dev Tools\Screenshots (by default)
Resized:      Pictures\CyberCute Dev Tools\Resized (by default)

If you used a build named GameDevTools, its settings are copied the first time
this version starts. Output folders you had selected stay selected, and the old
%APPDATA%\GameDevTools folder is left in place. You may delete it afterwards.

Update
------
The app does not check for updates. To update, close it and replace
cybercute-dev-tools.exe with the one from the new ZIP. Your settings are kept.

Uninstall
---------
Close the app and delete the folder you extracted. To remove your settings and
logs too, delete the two CyberCute Dev Tools folders listed above. Your
screenshots and resized images are never removed.
