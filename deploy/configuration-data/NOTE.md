Unfortunatelly, our certificates do not work since we need to add the IP of the kubelet to the SAN. The base virtual kubelet handles this using a special helm chart that generates the certificates.

To fix this, our kubelet needs to create the certificates when the pod is launched. We are not doing this which generates errors while calling the HTTPs API. This can be an issue in the future.

Examples:

```
$ curl "https://10.1.216.190:10250/containerLogs/default/temperature-deployment-9c88949fb-cqb4g/temperature?tailLines=5000&timestamps=true" 
curl: (60) SSL certificate problem: self-signed certificate
More details here: https://curl.se/docs/sslcerts.html

curl failed to verify the legitimacy of the server and therefore could not
establish a secure connection to it. To learn more about this situation and
how to fix it, please visit the web page mentioned above.
$

>>> Kubelet Log

2025/01/17 18:09:12 http: TLS handshake error from 172.16.5.174:33016: local error: tls: bad record MAC

```

If we tell curl to ignore the certificate, everything works:

```

$ curl "https://10.1.216.190:10250/containerLogs/default/temperature-deployment-9c88949fb-cqb4g/temperature?tailLines=5000&timestamps=true" -k
$

>>> Kubelet Log

time="2025-01-17T18:10:23Z" level=info msg="receive GetContainerLogs \"temperature-deployment-9c88949fb-cqb4g\"" containerName=temperature method=GetContainerLogs name=temperature-deployment-9c88949fb-cqb4g namespace=default uri="/containerLogs/default/temperature-deployment-9c88949fb-cqb4g/temperature?tailLines=5000&timestamps=true" user-id= user-name="system:anonymous" vars="map[]"

```