# MMSGate2

## Introduction

MMSGate2 is a MMS message gateway between [VoIP.ms](https://voip.ms/) and [Linphone](https://www.linphone.org/en/) clients.

Linphone is an open-source soft-phone. It makes VoiP SIP calls and can send/receive SMS/MMS messages over the SIP protocol. It can also use push notifications to ensure no calls are missed and SMS/MMS are delivered quickly.

VoIP.ms provides voice and SMS over SIP protocol. While MMS messages are possible, the service is provided over a customized API and web hook. MMSGate2 provides the link between VoIP.ms's MMS service and Linphone clients.

The Linphone clients connect through [OpenSIPS](https://www.opensips.org/) to VoIP.ms via SIP protocols. MMSgate2 communicates via SIP and web interfaces with VoIP.ms and Linphone clients.  Calls are established via the standard SIP protocol.  However, the voice data is sent directly between VoIP.ms and the Linphone clients.  Optional Push Notifications are sent via standard SIP messages, utilizing the existing Linphone infrastructure.   

MMSGate2 relies on VoIP.ms sub accounts and their extensions.  When clients are configured, it includes contacts for the extensions.  Messages and calls between extensions are free of charge.  All communications are encrypted.  Messages between extensions are passed directly from client to client via MMSGate2.  

![Diagram](images/mmsgate2-system.png)

## Requirements and Prerequisites

MMSGate2 has a very small footprint, as little as 100 megabytes of memory.  This allows it to run on residential routers with [OpenWRT](https://openwrt.org/) installed or a very low cost VPS servers.  It can also run on a pocket sized hobby computer like a [Raspberry Pi Zero W](https://www.raspberrypi.com/products/raspberry-pi-zero-w/).  As long as the system can run [Docker](https://www.docker.com/); Docker Community Edition (CE) or Docker-Desktop; it can run MMSGate2.  That includes Raspberry Pi devices and laptops or desktops running Windows, Ubuntu or even MacOS.  Many NAS devices that you may already own can also run Docker.  

If you can download and install software, type URLs into a web page, copy-and-paste text, you can install Docker and MMSGate2.

## Prepare the Host

The host is the device you will install Docker.  MMSGate2 will run within Docker.  The host preparation will be different for different hosts.  Let's look at each one:

<details >
<summary>Windows</summary>
For any Windows system, start by downloading Docker-Desktop for Windows 
from <a href='https://www.docker.com/'>https://www.docker.com/</a>.  You
will most likely want the AMD64 version.  <br><br>
Once installed, open the Docker-Desktop application.  Confirm that it 
says "Engine Started" in the lower left.  It may take a little while.  If 
it does not appear, you may have to reboot and try again.  <br><br>
Once the engine is started, click on the ">_ Terminal" prompt in the 
lower-right of Docker Desktop.  A terminal window will appear.  This is 
where you will be pasting commands.  <br><br>
Tip: Closing the Docker Desktop window does not stop the Docker engine.  
If you wish to stop Docker completely, use the taskbar in the lower-right 
of the Windows desktop.  Right-click on Docker Desktop and select 
Quit Docker Desktop.  
</details>

<details >
<summary>Apple MacOS</summary>
For any MacOS system, start by downloading Docker-Desktop for Mac from
<a href='https://www.docker.com/'>https://www.docker.com/</a>.  You
likely know of you need the Intel or Sillicon version.  If not, try 
one.  It it won't install, try the other. <br><br>
Once installed, open the Docker-Desktop application.  Confirm that it 
says "Engine Started" in the lower left.  It may take a little while.  <br><br>
Once the engine is started, click on the ">_ Terminal" prompt in the 
lower-right of Docker Desktop.  A terminal window will appear.  This is 
where you will be pasting commands.  <br><br>
Tip: Closing the Docker Desktop window does not stop the Docker engine.  
If you wish to stop Docker completely, You can open the Activity Monitor, 
select Docker, and then use the Quit button.  
</details>

<details >
<summary>Ubuntu Desktop</summary>
For most any Linux GUI desktop system, start by selecting Download 
Docker-Desktop, Download for Linux from 
<a href='https://www.docker.com/'>https://www.docker.com/</a>.  It will 
take you to the steps needed for your distribution.<br><br>
Once installed, open the Docker-Desktop application.  Confirm that it 
says "Engine Started" in the lower left.  It may take a little while.  <br><br>
Once the engine is started, click on the ">_ Terminal" prompt in the 
lower-right of Docker Desktop.  A terminal window will appear.  This is 
where you will be pasting commands.  <br><br>
Tip: Closing the Docker Desktop window does not stop the Docker engine.  
If you wish to stop Docker completely, You can open the Activity Monitor, 
click Docker, and then select Quit Docker Desktop.  
</details>

<details >
<summary>OpenWRT</summary>
<a href='https://openwrt.org/'>OpenWRT</a> runs a wide variety of hardware.  
If it has enough memory, i.e. 100m free; also ARM32 (v7+) or 
ARM64 processor or an AMD64 (Intel x86); plus some storage; you can 
install Docker.  <br><br>
First, make sure you have storage.  Don't try to run Docker on your 
internal flash storage.  Read 
<a href='https://openwrt.org/docs/guide-user/storage/usb-drives'>Using 
storage devices</a> to get started.  Then setup the
<a href='https://openwrt.org/docs/guide-user/additional-software/extroot_configuration'>
Extroot configuration</a>.  <br><br>
To install Docker, open a SSH session and paste these commands:
<pre><code>opkg update
opkg install docker
opkg install luci-app-dockerman
</code></pre><br><br>
The default network for docker has some issues, so we'll create a new one:
<pre><code>uci show network
uci set network.docker1='device'
uci set network.docker1.type='bridge'
uci set network.docker1.name='docker1'
uci set network.dockerlan='interface'
uci set network.dockerlan.proto='none'
uci set network.dockerlan.device='docker1'
uci set network.dockerlan.auto='0'
uci commit network
/etc/init.d/network reload
# tell docker about new network
docker network create -o com.docker.network.bridge.enable_icc=true -o com.docker.network.bridge.enable_ip_masquerade=true \
  dockerlan -o com.docker.network.bridge.host_binding_ipv4=0.0.0.0 -o com.docker.network.bridge.name=docker1 \
  --ip-range=172.19.0.0/16 --subnet 172.19.0.0/16 --gateway=172.19.0.1
</code></pre><br><br>
Next, we need some firewall settings:
<pre><code>uci show firewall
# create a zone for docker
uci set firewall.docker=zone
uci set firewall.docker.name='docker'
uci set firewall.docker.input='ACCEPT'
uci set firewall.docker.output='ACCEPT'
uci set firewall.docker.forward='ACCEPT'
uci set firewall.docker.network='docker'
uci add_list firewall.docker.network='dockerlan'
uci set firewall.docker.device='docker0'
uci add_list firewall.docker.device='docker1'
# docker can talk to all and lan to docker, but wan can't talk to docker
uci set firewall.docker2lan.src='docker'
uci set firewall.docker2lan.dest='lan'
uci set firewall.lan2docker=forwarding
uci set firewall.lan2docker.src='lan'
uci set firewall.lan2docker.dest='docker'
uci set firewall.docker2wan=forwarding
uci set firewall.docker2wan.src='docker'
uci set firewall.docker2wan.dest='wan'
# open ports for mmsgate2
uci set firewall.mmsgate2=rule
uci set firewall.mmsgate2.src='wan'
uci set firewall.mmsgate2.name='mmsgate2'
uci set firewall.mmsgate2.dest_port='5061 38443'
uci set firewall.mmsgate2.target='ACCEPT'
uci set firewall.mmsgate2.dest='*'
# good to be paranoid
uci set firewall.mmsgate2noadm=rule
uci set firewall.mmsgate2noadm.src='wan'
uci set firewall.mmsgate2noadm.name='mmsgate2noadm'
uci set firewall.mmsgate2noadm.target='REJECT'
uci set firewall.mmsgate2noadm.dest_port='38000'
uci set firewall.mmsgate2noadm.dest='*'
uci commit firewall
/etc/init.d/firewall reload
</code></pre><br><br>
</details>

<details >
<summary>Raspberry Pi/VPS/Ubuntu Server</summary>
For all these devices, you'll need Ubuntu Server installed.  Once 
on the system, you can follow the guide 
<a href='https://docs.docker.com/engine/install/ubuntu'>Docker 
install Ubuntu</a>.  <br><br>
Tip for the Raspberry Pi:  These devices don't need many accessories.  
I have had good luck with just flashing Ubuntu Server onto the SD card 
with SSH enabled and network setup (including Wifi).  Put the SD card 
in the Pi, power it up and a few minutes later, I check the DHCP server 
in the router to get the Pi's IP address.  Then I just SSH to it.  
Thus, no keyboard or monitor needed.  <br><br>
Tip for VPS: The admin interface is not available except from via a 
private network such as 192.168.0.0/16 or 10.0.0.0/8.  A VPS may not have 
a private network. However, it is available locally within the container 
via port 38080.  Use the following command to open a web browser locally:
<pre><code>docker exec -it mmsgate2 lynx http://127.0.0.1:38080/admin
</code></pre><br><br>
</details>

<details >
<summary>NAS</summary>
NAS devices that are ARM or AMD (Intel) based have applications that you 
can install that run Docker; like have apps like 
<a href='https://www.qnap.com/en-us/software/container-station'>
QNAP's Container Station</a> or 
<a href='https://www.synology.com/en-br/dsm/feature/docker'>
Synology's Container Manager</a>.  <br><br>
The command prompt will still be needed, so once the app is install, 
enable SSH and open an SSH comand prompt.
Synology tips: Commands pasted into the SSH sessions will usually need a
sudo prefix.  This is because the default user via SSH may not have docker 
permission.  
</details>

**The host and up-time:**  For the long term, It is not recommended to run MMSGate2 on a system that has active power-management (can go to sleep) or is also used for web browsing, email and office documents.  For the long term, please use a host that will be up and operational 24/7.  

## Install MMSGate2

Open the Docker Command Line Interface (CLI).  For Docker-Desktop, that is the ">_ Terminal" in the lower right of Docker-Desktop.  For OpenWRT, NAS and Ubuntu Servers, that is the SSH session.  

Copy-and-paste the following command into the Docker CLI:

`docker pull rvgo4it/mmsgate2`

You will see multiple download and extracts for the image layers.  It may take a few minutes.  Once done, we can run MMSGate2.  

The the following command, make note of some options you may want to change.  For example, to give MMSGate2 more memory, adjust the "-m 100m" to "-m 200m" for 200 megs of memory as an example.  You can adjust the "--cpus 2" option to use more CPU cores.  However, more cores means more processes and more processes need more memory.  Also note the "TZ=America/New_York" value.  Depending on your time zone, you may want to change it to "TZ=America/Chicago", "TZ=America/Los_Angeles" or "TZ=America/Denver".  

For Windows, use this command:

```
docker run -m 100m --name mmsgate2 -d `
  -p 5061:5061 -p 38443:38443 -p 38000:38000 `
  --cpus 2 `
  -e "TZ=America/New_York" `
  -v datavol:/data `
  -v confvol:/etc/opensips `
  rvgo4it/mmsgate2 
```

For OpenWRT, use this command:

```
docker run -m 100m --name mmsgate2 -d \
  --cpus 2 --network dockerlan \
  -p 5061:5061 -p 38443:38443 -p 38000:38000 \
  -e "TZ=America/New_York" \
  -v datavol:/data \
  -v confvol:/etc/opensips \
  rvgo4it/mmsgate2
```

For MacOS, NAS and Ubuntu server and desktop, use this command:

```
docker run -m 100m --name mmsgate2 -d \
  -p 5061:5061 -p 38443:38443 -p 38000:38000 \
  --cpus 2 \
  -e "TZ=America/New_York" \
  -v datavol:/data \
  -v confvol:/etc/opensips \
  rvgo4it/mmsgate2
```

## Configure MMSGate2

MMSGate2 is now running, but it needs to be configured for you.

### Connect

Open your favorite web browser and connect using http to the host's IP, port 38000 and path /admin.  For Docker-Desktop users, you can use 127.0.0.1 for the local host.  Thus, the URL will be:

`http://127.0.0.1:38000/admin`

For others, use the IP address you used for SSH.  It may be something like:

`http://192.168.99.99:38000/admin`

It will prompt for an ID and password:  

    Username:     admin
    Password:     Apple99

It will look something like this:

![](images/mainmenu.png)

### Password Change

**IMPORTANT: **Change the admin password right away.  It is under Advanced->Set_Admin_Password.  

![](images/password.png)

Enter the password twice and click Apply.  

Once done, return to the main menu.

### Wizard

Click Wizard. The Wizard will walk you through the setup of MMSGate2 one step at a time.  For OpenWRT hosts, no router configuration needed.  That step in the Wizard can be skipped.  Once the Wizard is completed, you will return to the main menu.

### Linphone Accounts

If you want to use Push Notifications, click Linphone.

![](images/linphone.png)

You can create new linphone accounts from this page or add existing one.  Once they are created or added successfully, they will appear as "Activated!" and can be used for Push Notifications.  

You will need one Linphone account for each VoIP.ms sub account used by a mobile device running the Linphone App and Push Notification is needed.  When done, click Cancel to return to the main menu.

### VoIP.ms Sub Accounts

From the main menu, select VoIP.ms.  

![](images/voipms.png)

This page displays all your sub accounts.  It will not make any changes to VoIP.ms.  

It may ask you to make changes to your sub accounts if needed.  MMSGate needs a DID selected as the CallerID for any sub accounts.  Also, the sub account needs to be configured for encrypted SIP traffic and be assigned a unique extension.  Any DID used with MMSGate2 also needs to have a web hook URL entered.  This page will tell you if needed.  After making changes to sub accounts or DIDs at the Voip.ms web site, click Refresh to load the new settings.   

The SMS/MMS Ignore/Accept is for when a message arrives on one of your DIDs.  It can be accepted and forwarded to any sub accounts associated with that DID.  Or, the message can be ignored.  

Under Push Notif, a Linphone account can be selected for the sub account.  If selected, Push Notifications can be used by the mobile app.  

When modifying Push Notif or SMS/MMS settings for a sub account, press Apply after making selection.  

Once done settings preferences, click "Client Config".

### Client Config

![](images/client.png)

By default, the client config includes an encrypted copy of the sub account password.  Also, account configs are loaded into the mobile app starting at zero.  If there is already an account at zero, loading this new config will overwrite it.  To prevent overwriting, you can change the index.  If the password preference or index is changed, press Refresh.

The QR code is the easiest way to configure the client.  Install a Linphone client.

- Android - [Linphone - Apps on Google Play](https://play.google.com/store/apps/details?id=org.linphone&pli=1)

- Android - [Linphone - F-Droid - Free and Open Source Android App Repository](https://f-droid.org/packages/org.linphone/)
  
  - Note: Linphone installed from F-Droid cannot use Push Notification

- Apple iOS - [‎Linphone App - App Store](https://apps.apple.com/us/app/linphone/id360065638)

- Apple MacOS - [Download](https://linphone.org/releases/macosx/latest_app)

- Windows - [Download](https://linphone.org/releases/windows/latest_app_win64)

- Linux - [Download](https://linphone.org/releases/linux/latest_app)

Once the app is installed, open it and respond to the usual prompts.  Stop short of registering or providing any credentials.  When offered, use the camera to scan the QR code.   Once scanned, logon is done and you are online.  

Some clients cannot scan a QR code.  For them, you will need to copy-and-paste the XML Config URL into the client.  
