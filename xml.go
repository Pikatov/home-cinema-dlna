package main

import (
	"strings"
)

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func escapeXMLText(s string) string {
	return xmlEscaper.Replace(s)
}

func escapeXMLAttr(s string) string {
	return xmlEscaper.Replace(s)
}

const deviceDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0">
	<specVersion><major>1</major><minor>0</minor></specVersion>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
		<friendlyName>%s</friendlyName>
		<manufacturer>%s</manufacturer>
		<modelName>%s</modelName>
		<UDN>uuid:%s</UDN>
		<dlna:X_DLNADOC xmlns:dlna="urn:schemas-dlna-org:device-1-0">DMS-1.50</dlna:X_DLNADOC>
		<serviceList>
			<service>
				<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
				<SCPDURL>/desc.xml</SCPDURL>
				<controlURL>/ctl/ContentDirectory</controlURL>
				<eventSubURL>/evt/ContentDirectory</eventSubURL>
			</service>
		</serviceList>
	</device>
</root>`

const soapResponse = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>&lt;didl-lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/"&gt;%s&lt;/didl-lite&gt;</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>%d</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`
