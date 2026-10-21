import dns from "node:dns";
import net from "node:net";

// Provider calls failed in bursts with ETIMEDOUT about 1.3 s apart (docs/plans/odysseys-campaign.md):
// Node races IPv4 and IPv6 with 250 ms for the first address, so a slow moment on a busy host fell
// through to IPv6, which containers often cannot route. IPv4 goes first and gets the whole connect.
dns.setDefaultResultOrder("ipv4first");
net.setDefaultAutoSelectFamily(false);
