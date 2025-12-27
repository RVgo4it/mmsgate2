# MMSGate2

## <a name='voip.ms' href='https://voip.ms'></a><a name='linphone' href='https://www.linphone.org/en/'></a><a name='opensips' href='https://www.opensips.org/'></a>Intro

MMSGate2 is a MMS message gateway between [VoIP.ms](#voip.ms) and [Linphone](#linphone) clients.

Linphone is an open-source soft-phone. It makes VoiP SIP calls and can send/receive SMS/MMS messages over the SIP protocol. It can also use push notifications to ensure no calls are missed and SMS/MMS are delivered quickly.

VoIP.ms provides voice and SMS over SIP protocol. While MMS messages are possible, the service is provided over a customized API and web hook. MMSGate2 provides the link between VoIP.ms's MMS service and Linphone clients.

The Linphone clients connect through [OpenSIPS](#opensips) to VoIP.ms via SIP protocols. MMSgate2 communicates via SIP and web interfaces with VoIP.ms and Linphone clients.  Calls are established via the standard SIP protocol.  However, the voice data is sent directly between VoIP.ms and the Linphone clients.  Optional Push Notifications are sent via standard SIP messages, utilizing the existing Linphone infrastructure.   

![Diagram](images/mmsgate2-system.png)

## <a name='openwrt' href='https://openwrt.org/'></a>Requirements and Prerequisites

MMSGate2 has a very small footprint, as little as 100 megabytes of memory.  This allows it to run on residential routers with [OpenWRT](openwrt) or very low cost VPS servers.  As long as the system can run Docker.  