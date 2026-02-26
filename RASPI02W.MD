# Raspberry Pi Zero 2 W with Ubuntu

To setup the Pi to act as a host, made sure you have these:

- [ ] Pi: Purchase it via [raspberrypi.com](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/).  

- [ ] Case: A case is not required, but it is recommended.  Some cases come with heat sink to help keep your Pi a little cooler.  

- [ ] Power supply: A micro USB power supply with at least 2.5A at 5v of power.  Some cell phone chargers are compatible.  You may have one already. 

- [ ] SD Card: You need a 32GB or larger micro SD card.  MMS messages need a place to store the media file.  More messages, more storage needed.  Recommend a Kingston or SanDIsk.  You may already have one.  

- [ ] Adapter: Make sure your desktop/laptop computer has a SD card reader/writer.  If not, you can purchase a USB to Micro SD adapter for about $10.  

- [ ] Download and Install: The Raspberry Pi Imager software.  It will write the Ubuntu server software to the SD card.  Get it from from [raspberrypi.com/software](https://www.raspberrypi.com/software/).  

Follow these steps to install Ubuntu:

1. Insert the SD card into your laptop/desktop reader or adapter.  

2. Open the Raspberry Pi Imager application.

3. Select your device as the Raspberry Pi Zero 2 W.

4. Select the OS, under "Other General-purpose OS", select the Ubuntu Server for ARM64 and latest LTS version.  

5. Select the storage device as the SD card you inserted in step 1.

6. Select Customization and enter the following:
   
   1. A host name, i.e. "raspi02w"
   
   2. Localization for timezone and keyboard type (despite no keyboard!)
   
   3. User name and password for logon
   
   4. Enter Wifi SSID and password.  It needs to be a for a 2.4GHz network.  If unsure, logon to your router to confirm.  Look for the wireless or Wifi settings section.  
   
   5. Enable SSH and use password authentication.

7. Write the image to the SD card

8. Once done, remove the SD card from laptop/desktop and insert into the Raspberry Pi.

9. Power on the Raspberry Pi.

10. Logon to your local router and check for current IP addresses.  It may be under DHCP or current clients or other.  

11. Look for the "raspi02w" name and note the IP address.  

12. Open your favorite SSH client and connect to the raspi02w IP address.

13. Logon using the ID and password set in step 6.3.  

14. Need some updates:
    
    ```bash
    sudo apt update && sudo apt upgrade
    ```

15. Need some power management software:
    
    ```bash
    sudo apt install iw
    ```

16. To disable power management, type this command:
    
    ```bash
    sudo iw wlan0 set power_save off
    ```

17. We'll schedule power save off at each reboot:
    
    ```bash
    sudo crontab -e
    ```

18. If asked about editor, select "nano".  Paste the following at the bottom:
    
    ```bash
    @reboot /usr/sbin/iw wlan0 set power_save off > /tmp/power_save_log.txt 2>&1
    ```

19. To save, press ctrl-s.  To exit, press ctrl-x.

20. Setup swap space using these commands:
    
    ```bash
    sudo fallocate -l 256M /swap.img
    sudo chmod 600 /swap.img
    sudo mkswap /swap.img
    sudo swapon /swap.img
    ```

21. To re-enable after next reboot:
    
    ```bash
    sudo nano /etc/fstab
    ```

22. Paste the following at the bottom of the file:
    
    ```bash
    /swap.img swap swap defaults 0 0
    ```

23. Save and exit

24. Adjust the Swappiness value lower so as to not use the swap file unless needed.  Ubuntu default is 60.  We'll set it to 30:
    
    ```bash
    sudo sysctl vm.swappiness=30
    ```

25. To make sure it remembers after reboot, open sysctl.conf:
    
    ```bash
    sudo nano /etc/sysctl.conf
    ```

26. Paste this line at the bottom:
    
    ```bash
    vm.swappiness=30
    ```

27. Save and exit

28. Reboot and confirm everything is set:
    
    ```bash
    sudo shutdown --reboot now
    ```

29. After a minute or two, reconnect via SSH and logon.

30. Type these commands to confirm settings:
    
    ```bash
    iw wlan0 get power_save
    free -h
    cat /proc/sys/vm/swappiness
    ```

31. If it looks good, you are all set!
