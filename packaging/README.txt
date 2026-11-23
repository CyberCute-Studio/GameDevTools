CyberCute Dev Tools
===================

A Windows 11 (64-bit) desktop toolbox for exact-size screenshots and batch
image resizing. It works offline and needs no administrator rights.

USER-GUIDE.txt explains how to use it, its limitations, and what to do when
something goes wrong. RELEASE-NOTES.txt says what is in this version and what
has not been verified yet. CyberCute Dev Tools is released under the MIT
License, in LICENSE.txt; THIRD-PARTY-NOTICES.txt covers the components it
includes.

Run it
------
Extract the whole ZIP to a folder you can write to, such as Documents. It
creates a CyberCute Dev Tools folder there; open cybercute-dev-tools.exe inside
it. Nothing is installed. Pin it to Start or the taskbar if you want a shortcut.

This build is not code-signed, so Windows SmartScreen may say it does not
recognize the app. Check the download first (below), then choose
More info > Run anyway.

Check the download
------------------
Each release publishes a .sha256 file beside the ZIP. In PowerShell:

    (Get-FileHash .\cybercute-dev-tools-<version>-windows-x64.zip -Algorithm SHA256).Hash.ToLower()

The result must match the first value in the .sha256 file.

Your files
----------
Settings:     %APPDATA%\CyberCute Dev Tools
Logs:         %LOCALAPPDATA%\CyberCute Dev Tools\Logs
Screenshots:  Pictures\CyberCute Dev Tools\Screenshots (by default)
Resized:      Pictures\CyberCute Dev Tools\Resized (by default)

If you used a build named GameDevTools, its settings are copied the first time
this version starts, and the old %APPDATA%\GameDevTools folder is left in
place. You may delete it afterwards. While it exists, its settings are copied
again whenever the new settings file is missing. If they cannot be copied, the
app says so and saves no changes in that session; your settings stay in the old
folder, and the next start tries again.

Output folders stored in those settings stay selected. That build stored the
folder in use whenever you changed a capture or resize option, so if you changed
any, that tool keeps saving to Pictures\GameDevTools until you choose another
folder. A tool whose options you never changed uses the default folder above.
Images you already saved stay where they are.

Update
------
The app does not check for updates. To update, close it and extract the new ZIP
to the folder that holds your CyberCute Dev Tools folder, such as Documents, not
into that folder itself. Windows' Extract All suggests a new folder named after
the ZIP, so change the destination. Replace the existing files when asked, so
that the guide and release notes match the new version. If you renamed your
folder, extract the ZIP somewhere else and copy the files from its CyberCute Dev
Tools folder into yours instead, so that a pinned shortcut runs the new version.
Your settings are kept.

Uninstall
---------
Close the app and delete the folder you extracted. To remove your settings and
logs too, delete %APPDATA%\CyberCute Dev Tools and
%LOCALAPPDATA%\CyberCute Dev Tools and, if you used a build named GameDevTools,
%APPDATA%\GameDevTools and %LOCALAPPDATA%\GameDevTools. Leave the folders in
Pictures: they hold your screenshots and resized images, which uninstalling
does not remove.
